package management

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	requestLogPreviewDefault = 64 * 1024
	requestLogPreviewMaximum = 1024 * 1024
)

// The logger retains path punctuation (including thinking-budget parentheses)
// and Unicode. Reject separators/control characters rather than restricting the
// path prefix to ASCII identifiers; the timestamp and request ID stay structural.
var requestLogFilenamePattern = regexp.MustCompile(`^[^\x00-\x1f\x7f<>:"|?*\\/\s]+-(\d{4}-\d{2}-\d{2}T\d{6})-[A-Za-z0-9_.-]+\.log$`)

type requestLogMetadata struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Kind     string `json:"kind"`
}

type requestLogPreview struct {
	Name       string `json:"name"`
	Text       string `json:"text"`
	NextOffset int64  `json:"next_offset"`
	Size       int64  `json:"size"`
	Modified   int64  `json:"modified"`
	HasMore    bool   `json:"has_more"`
}

// requestLogKind matches generateFilename in logging/request_logger_writer.go.
func requestLogKind(name string) (string, bool) {
	if name == defaultLogFileName || isRotatedLogFile(name) {
		return "", false
	}
	match := requestLogFilenamePattern.FindStringSubmatch(name)
	if match == nil {
		return "", false
	}
	if _, errParse := time.Parse("2006-01-02T150405", match[1]); errParse != nil {
		return "", false
	}
	if strings.HasPrefix(name, "error-") {
		return "error", true
	}
	return "request", true
}

func closeRequestLogResource(resource io.Closer) {
	if errClose := resource.Close(); errClose != nil {
		log.WithError(errClose).Warn("Failed to close request log inspection resource")
	}
}

func (h *Handler) requestLogRoot(c *gin.Context) *os.Root {
	if h == nil || h.configSnapshot() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return nil
	}
	dir := h.logDirectory()
	if strings.TrimSpace(dir) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "log directory not configured"})
		return nil
	}
	root, errOpen := os.OpenRoot(dir)
	if errOpen != nil {
		if os.IsNotExist(errOpen) {
			c.JSON(http.StatusNotFound, gin.H{"error": "request log file not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "request log directory unavailable"})
		}
		return nil
	}
	return root
}

// ListRequestLogs includes persisted request and error logs regardless of current logging settings.
func (h *Handler) ListRequestLogs(c *gin.Context) {
	if h == nil || h.configSnapshot() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	dir := h.logDirectory()
	if strings.TrimSpace(dir) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "log directory not configured"})
		return
	}
	entries, errRead := os.ReadDir(dir)
	if errRead != nil && !os.IsNotExist(errRead) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request log directory unavailable"})
		return
	}
	files := make([]requestLogMetadata, 0)
	for _, entry := range entries {
		kind, allowed := requestLogKind(entry.Name())
		if !allowed || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, errInfo := entry.Info()
		// Rotation can remove entries while the directory is being inspected.
		if errInfo != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, requestLogMetadata{
			Name: entry.Name(), Size: info.Size(), Modified: info.ModTime().Unix(), Kind: kind,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Modified == files[j].Modified {
			return files[i].Name < files[j].Name
		}
		return files[i].Modified > files[j].Modified
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"files": files})
}

// openRequestLog anchors the path and verifies the opened inode, rejecting symlinks and file swaps.
func (h *Handler) openRequestLog(c *gin.Context) (*os.File, os.FileInfo) {
	name := c.Param("name")
	if name == "" || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request log filename"})
		return nil, nil
	}
	if _, allowed := requestLogKind(name); !allowed {
		c.JSON(http.StatusNotFound, gin.H{"error": "request log file not found"})
		return nil, nil
	}
	root := h.requestLogRoot(c)
	if root == nil {
		return nil, nil
	}
	defer closeRequestLogResource(root)
	before, errBefore := root.Lstat(name)
	if errBefore != nil || !before.Mode().IsRegular() {
		c.JSON(http.StatusNotFound, gin.H{"error": "request log file not found"})
		return nil, nil
	}
	file, errOpen := root.Open(name)
	if errOpen != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "request log file not found"})
		return nil, nil
	}
	after, errAfter := file.Stat()
	current, errCurrent := root.Lstat(name)
	if errAfter != nil || errCurrent != nil || !after.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(before, after) || !os.SameFile(current, after) {
		closeRequestLogResource(file)
		c.JSON(http.StatusNotFound, gin.H{"error": "request log file not found"})
		return nil, nil
	}
	return file, after
}

func requestLogPageQuery(c *gin.Context) (int64, int, bool) {
	query := c.Request.URL.Query()
	for key, values := range query {
		if (key != "offset" && key != "limit") || len(values) != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request log pagination"})
			return 0, 0, false
		}
	}
	offset := int64(0)
	limit := requestLogPreviewDefault
	if value, exists := query["offset"]; exists {
		parsed, errParse := strconv.ParseInt(value[0], 10, 64)
		if errParse != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be a non-negative byte position"})
			return 0, 0, false
		}
		offset = parsed
	}
	if value, exists := query["limit"]; exists {
		parsed, errParse := strconv.Atoi(value[0])
		if errParse != nil || parsed < 1 || parsed > requestLogPreviewMaximum {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 1048576 bytes"})
			return 0, 0, false
		}
		limit = parsed
	}
	return offset, limit, true
}

func requestLogUTF8Boundary(file *os.File, offset int64) bool {
	start := max(int64(0), offset-(utf8.UTFMax-1))
	buffer := make([]byte, offset-start+utf8.UTFMax)
	n, _ := file.ReadAt(buffer, start)
	buffer = buffer[:n]
	for index := 0; index < len(buffer) && int64(index) < offset-start; index++ {
		_, size := utf8.DecodeRune(buffer[index:])
		if size > 1 && int64(index+size) > offset-start {
			return false
		}
	}
	return true
}

// GetRequestLogPreview returns one byte-addressed page; a complete trailing UTF-8 rune can add up to three bytes.
func (h *Handler) GetRequestLogPreview(c *gin.Context) {
	offset, limit, valid := requestLogPageQuery(c)
	if !valid {
		return
	}
	file, info := h.openRequestLog(c)
	if file == nil {
		return
	}
	defer closeRequestLogResource(file)
	if offset > info.Size() || !requestLogUTF8Boundary(file, offset) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be within the file at a UTF-8 character boundary"})
		return
	}
	length := min(int64(limit+utf8.UTFMax-1), info.Size()-offset)
	buffer := make([]byte, int(length))
	n, errRead := file.ReadAt(buffer, offset)
	if errRead != nil && !errors.Is(errRead, io.EOF) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request log file could not be read"})
		return
	}
	buffer = buffer[:n]
	end := min(limit, len(buffer))
	if end < len(buffer) && !utf8.RuneStart(buffer[end]) {
		for start := end - 1; start >= max(0, end-(utf8.UTFMax-1)); start-- {
			_, size := utf8.DecodeRune(buffer[start:])
			if size > 1 && start+size > end {
				end = start + size
				break
			}
		}
	}
	nextOffset := offset + int64(end)
	// A concurrently truncated file must not produce an endless empty page.
	size := info.Size()
	if int64(n) < length {
		size = offset + int64(n)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, requestLogPreview{
		Name: c.Param("name"), Text: string(buffer[:end]), NextOffset: nextOffset,
		Size: size, Modified: info.ModTime().Unix(), HasMore: nextOffset < size,
	})
}

// DownloadRequestLog streams the entire opened file without a preview size limit.
func (h *Handler) DownloadRequestLog(c *gin.Context) {
	file, info := h.openRequestLog(c)
	if file == nil {
		return
	}
	defer closeRequestLogResource(file)
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Type", "application/octet-stream")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": c.Param("name")}))
	http.ServeContent(c.Writer, c.Request, c.Param("name"), info.ModTime(), file)
}
