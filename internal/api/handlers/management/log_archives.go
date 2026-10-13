package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	log "github.com/sirupsen/logrus"
)

type logArchiveReader interface {
	Close()
	List(context.Context, string, int) (logarchive.ListResult, error)
	Open(context.Context, string) (io.ReadCloser, logarchive.Object, error)
}

type logArchiveEntry struct {
	Source   string `json:"source"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

type logArchiveCursor struct {
	Source string `json:"source"`
	After  string `json:"after"`
}

func (h *Handler) newLogArchiveReader(cfg config.LogArchiveConfig) (logArchiveReader, error) {
	if h.logArchiveReaderFactory != nil {
		return h.logArchiveReaderFactory(cfg)
	}
	return logarchive.NewStore(cfg)
}

func archiveLogKind(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, "/\\\x00\r\n") {
		return "", false
	}
	plain := strings.TrimSuffix(name, ".gz")
	if plain == defaultLogFileName || isRotatedLogFile(plain) {
		return "application", true
	}
	if strings.HasPrefix(plain, "recovered-") && logarchive.IsCompletedLogName(plain) {
		return "recovered", true
	}
	return requestLogKind(plain)
}

// GetLogArchives pages through local logs followed by permanent S3 archives.
// Listings are live: a log can move between sources while a caller is paging.
func (h *Handler) GetLogArchives(c *gin.Context) {
	logging.SkipGinRequestLogging(c)
	cfg := h.configSnapshot()
	if cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	limit := 100
	if raw, exists := c.GetQuery("limit"); exists {
		parsed, errParse := strconv.Atoi(raw)
		if errParse != nil || parsed < 1 || parsed > 1000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 1000"})
			return
		}
		limit = parsed
	}
	cursor := logArchiveCursor{Source: "local"}
	if raw := c.Query("cursor"); raw != "" {
		if len(raw) > 4096 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid archive cursor"})
			return
		}
		data, errDecode := base64.RawURLEncoding.DecodeString(raw)
		if errDecode != nil || json.Unmarshal(data, &cursor) != nil ||
			(cursor.Source != "local" && cursor.Source != "s3") ||
			(cursor.Source == "local" && cursor.After != "" && !validLocalArchiveName(cursor.After)) ||
			(cursor.Source == "s3" && (len(cursor.After) > 1024 || strings.ContainsAny(cursor.After, "\x00\r\n"))) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid archive cursor"})
			return
		}
	}
	files := make([]logArchiveEntry, 0, limit)
	next := ""
	if cursor.Source == "local" {
		local, more, errList := listLocalLogArchives(h.logDirectory(), cursor.After, limit)
		if errList != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "log directory unavailable"})
			return
		}
		files = append(files, local...)
		if more {
			next = encodeLogArchiveCursor("local", local[len(local)-1].Key)
		} else if cfg.LogArchive.Enabled {
			next = encodeLogArchiveCursor("s3", "")
		}
		// Do not let an S3 outage hide pending local downloads. Remote pages
		// are separate when local entries exist, even if the page is short.
		if len(files) > 0 || next == "" {
			c.Header("Cache-Control", "no-store")
			c.JSON(http.StatusOK, gin.H{"files": files, "next_cursor": next, "archive_enabled": cfg.LogArchive.Enabled})
			return
		}
		cursor = logArchiveCursor{Source: "s3"}
	}
	if !cfg.LogArchive.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "S3 log archival is disabled"})
		return
	}
	store, errStore := h.newLogArchiveReader(cfg.LogArchive)
	if errStore != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "S3 log archive is unavailable"})
		return
	}
	defer store.Close()
	page, errList := store.List(c.Request.Context(), cursor.After, limit)
	if errList != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to list S3 log archives"})
		return
	}
	for _, object := range page.Objects {
		original, valid := logarchive.OriginalName(object.Key)
		kind, allowed := archiveLogKind(original)
		if !valid || !allowed {
			continue
		}
		files = append(files, logArchiveEntry{Source: "s3", Key: object.Key, Name: original + ".gz", Kind: kind, Size: object.Size, Modified: object.LastModified.Unix()})
	}
	if page.NextAfter != "" {
		next = encodeLogArchiveCursor("s3", page.NextAfter)
	} else {
		next = ""
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"files": files, "next_cursor": next, "archive_enabled": cfg.LogArchive.Enabled})
}

func validLocalArchiveName(name string) bool {
	_, ok := archiveLogKind(name)
	return ok
}

func encodeLogArchiveCursor(source, after string) string {
	data, _ := json.Marshal(logArchiveCursor{Source: source, After: after})
	return base64.RawURLEncoding.EncodeToString(data)
}

// listLocalLogArchives bounds memory regardless of the number of pending files.
func listLocalLogArchives(dir, after string, limit int) ([]logArchiveEntry, bool, error) {
	root, errRoot := os.OpenRoot(dir)
	if os.IsNotExist(errRoot) {
		return nil, false, nil
	}
	if errRoot != nil {
		return nil, false, errRoot
	}
	defer closeRequestLogResource(root)
	file, errOpen := root.Open(".")
	if errOpen != nil {
		return nil, false, errOpen
	}
	defer closeRequestLogResource(file)
	files := make([]logArchiveEntry, 0, limit+1)
	for {
		entries, errRead := file.ReadDir(128)
		for _, entry := range entries {
			name := entry.Name()
			kind, allowed := archiveLogKind(name)
			if !allowed || name <= after || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, errInfo := entry.Info()
			if os.IsNotExist(errInfo) {
				continue
			}
			if errInfo != nil {
				return nil, false, errInfo
			}
			if !info.Mode().IsRegular() {
				continue
			}
			index := sort.Search(len(files), func(i int) bool { return files[i].Key >= name })
			if index > limit {
				continue
			}
			files = slices.Insert(files, index, logArchiveEntry{Source: "local", Key: name, Name: name, Kind: kind, Size: info.Size(), Modified: info.ModTime().Unix()})
			if len(files) > limit+1 {
				files = files[:limit+1]
			}
		}
		if errors.Is(errRead, io.EOF) {
			break
		}
		if errRead != nil {
			return nil, false, errRead
		}
	}
	more := len(files) > limit
	if more {
		files = files[:limit]
	}
	return files, more, nil
}

// DownloadLogArchive streams original gzip bytes; Content-Encoding is deliberately
// absent so HTTP clients save the archive instead of transparently decompressing it.
func (h *Handler) DownloadLogArchive(c *gin.Context) {
	logging.SkipGinRequestLogging(c)
	cfg := h.configSnapshot()
	if cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration unavailable"})
		return
	}
	key := c.Query("key")
	switch c.Query("source") {
	case "local":
		if !validLocalArchiveName(key) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid local log filename"})
			return
		}
		root, errRoot := os.OpenRoot(h.logDirectory())
		if errRoot != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "local log unavailable; check log-archives for an archived copy"})
			return
		}
		defer closeRequestLogResource(root)
		before, errBefore := root.Lstat(key)
		if errBefore != nil || !before.Mode().IsRegular() {
			c.JSON(http.StatusNotFound, gin.H{"error": "local log not found"})
			return
		}
		file, errOpen := root.Open(key)
		if errOpen != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "local log unavailable; check log-archives for an archived copy"})
			return
		}
		defer closeRequestLogResource(file)
		info, errStat := file.Stat()
		current, errCurrent := root.Lstat(key)
		if errStat != nil || errCurrent != nil || !info.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(before, info) || !os.SameFile(current, info) {
			c.JSON(http.StatusNotFound, gin.H{"error": "local log not found"})
			return
		}
		setLogArchiveDownloadHeaders(c, key)
		http.ServeContent(c.Writer, c.Request, key, info.ModTime(), file)
	case "s3":
		original, valid := logarchive.OriginalName(key)
		if !valid || !validLocalArchiveName(original) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid S3 archive key"})
			return
		}
		if !cfg.LogArchive.Enabled {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "S3 log archival is disabled"})
			return
		}
		store, errStore := h.newLogArchiveReader(cfg.LogArchive)
		if errStore != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "S3 log archive is unavailable"})
			return
		}
		defer store.Close()
		body, object, errOpen := store.Open(c.Request.Context(), key)
		if errOpen != nil {
			status := http.StatusBadGateway
			message := "failed to download S3 log archive"
			if errors.Is(errOpen, logarchive.ErrNotFound) {
				status, message = http.StatusNotFound, "S3 log archive not found"
			}
			c.JSON(status, gin.H{"error": message})
			return
		}
		defer closeRequestLogResource(body)
		setLogArchiveDownloadHeaders(c, original+".gz")
		c.Header("Content-Length", strconv.FormatInt(object.Size, 10))
		if _, errCopy := io.Copy(c.Writer, body); errCopy != nil {
			// Do not append an error body to a partially transferred archive.
			log.Warn("S3 log archive download interrupted")
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "source must be local or s3"})
	}
}

func setLogArchiveDownloadHeaders(c *gin.Context, name string) {
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Type", "application/octet-stream")
	if strings.HasSuffix(name, ".gz") {
		c.Header("Content-Type", "application/gzip")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
}
