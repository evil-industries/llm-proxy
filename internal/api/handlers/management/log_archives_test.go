package management

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
)

type archiveReaderStub struct {
	list func(context.Context, string, int) (logarchive.ListResult, error)
	open func(context.Context, string) (io.ReadCloser, logarchive.Object, error)
}

func (s archiveReaderStub) Close() {}

func (s archiveReaderStub) List(ctx context.Context, after string, limit int) (logarchive.ListResult, error) {
	return s.list(ctx, after, limit)
}
func (s archiveReaderStub) Open(ctx context.Context, key string) (io.ReadCloser, logarchive.Object, error) {
	return s.open(ctx, key)
}

func archiveTestHandler(t *testing.T, enabled bool, reader logArchiveReader) *Handler {
	t.Helper()
	h := &Handler{cfg: &config.Config{LoggingToFile: true, LogArchive: config.LogArchiveConfig{Enabled: enabled}}, logDir: t.TempDir()}
	h.logArchiveReaderFactory = func(config.LogArchiveConfig) (logArchiveReader, error) { return reader, nil }
	return h
}

func archiveRequest(handler gin.HandlerFunc, target string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(c)
	return response
}

func TestLogArchivesPagesLocalThenS3(t *testing.T) {
	key := strings.Repeat("a", 64) + "/" + inspectionRequestName + ".gz"
	calls := 0
	h := archiveTestHandler(t, true, archiveReaderStub{list: func(_ context.Context, after string, limit int) (logarchive.ListResult, error) {
		calls++
		if after != "" || limit != 1 {
			t.Fatalf("unexpected remote pagination: after=%q limit=%d", after, limit)
		}
		return logarchive.ListResult{Objects: []logarchive.Object{{Key: key, Size: 42, LastModified: time.Unix(123, 0)}}}, nil
	}})
	for _, name := range []string{inspectionRequestName, "main.log", "secret.log", inspectionRequestName + ".pending"} {
		if errWrite := os.WriteFile(filepath.Join(h.logDir, name), []byte("log"), 0600); errWrite != nil {
			t.Fatal(errWrite)
		}
	}
	if errLink := os.Symlink(filepath.Join(h.logDir, "main.log"), filepath.Join(h.logDir, inspectionErrorName)); errLink != nil {
		t.Fatal(errLink)
	}
	var page struct {
		Files          []logArchiveEntry `json:"files"`
		NextCursor     string            `json:"next_cursor"`
		ArchiveEnabled bool              `json:"archive_enabled"`
	}
	cursor := ""
	for i, expected := range []string{"main.log", inspectionRequestName, inspectionRequestName + ".gz"} {
		response := archiveRequest(h.GetLogArchives, "/?limit=1&cursor="+url.QueryEscape(cursor))
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
			t.Fatalf("page %d: %d %s", i, response.Code, response.Body)
		}
		if !page.ArchiveEnabled || len(page.Files) != 1 || page.Files[0].Name != expected {
			t.Fatalf("page %d: %+v", i, page)
		}
		if i < 2 && calls != 0 {
			t.Fatal("local pages must remain available without accessing S3")
		}
		cursor = page.NextCursor
	}
	if calls != 1 || cursor != "" || page.Files[0].Source != "s3" || page.Files[0].Kind != "request" {
		t.Fatalf("final page: %+v calls=%d", page, calls)
	}
}

func TestLogArchivesCursorAdvancesPastUnrelatedS3Objects(t *testing.T) {
	h := archiveTestHandler(t, true, archiveReaderStub{list: func(_ context.Context, after string, limit int) (logarchive.ListResult, error) {
		if after == "" {
			return logarchive.ListResult{NextAfter: "unrelated/object"}, nil
		}
		if after != "unrelated/object" {
			t.Fatalf("after=%q", after)
		}
		return logarchive.ListResult{}, nil
	}})
	response := archiveRequest(h.GetLogArchives, "/")
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if errDecode := json.Unmarshal(response.Body.Bytes(), &page); errDecode != nil {
		t.Fatal(errDecode)
	}
	response = archiveRequest(h.GetLogArchives, "/?cursor="+url.QueryEscape(page.NextCursor))
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
}

func TestDownloadLogArchivePreservesGzipBytes(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, errWrite := writer.Write(bytes.Repeat([]byte("request and response\n"), 10000)); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	key := strings.Repeat("b", 64) + "/" + inspectionRequestName + ".gz"
	closed := false
	h := archiveTestHandler(t, true, archiveReaderStub{open: func(ctx context.Context, requested string) (io.ReadCloser, logarchive.Object, error) {
		if requested != key {
			t.Fatalf("key=%q", requested)
		}
		return trackedArchiveBody{Reader: bytes.NewReader(compressed.Bytes()), closed: &closed}, logarchive.Object{Size: int64(compressed.Len())}, nil
	}})
	response := archiveRequest(h.DownloadLogArchive, "/?source=s3&key="+url.QueryEscape(key))
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), compressed.Bytes()) || !closed {
		t.Fatalf("download status=%d bytes=%d closed=%t", response.Code, response.Body.Len(), closed)
	}
	if response.Header().Get("Content-Encoding") != "" || response.Header().Get("Content-Type") != "application/gzip" || !strings.Contains(response.Header().Get("Content-Disposition"), inspectionRequestName+".gz") {
		t.Fatalf("headers=%v", response.Header())
	}
}

type trackedArchiveBody struct {
	io.Reader
	closed *bool
}

func (b trackedArchiveBody) Close() error { *b.closed = true; return nil }

func TestDownloadLocalArchiveRejectsSymlinksAndUnsafeNames(t *testing.T) {
	h := archiveTestHandler(t, false, nil)
	outside := filepath.Join(t.TempDir(), "private")
	if errWrite := os.WriteFile(outside, []byte("secret"), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errLink := os.Symlink(outside, filepath.Join(h.logDir, "main.log")); errLink != nil {
		t.Fatal(errLink)
	}
	for _, name := range []string{"../main.log", `..\main.log`, "secret.log", "main.log", inspectionRequestName + ".pending"} {
		response := archiveRequest(h.DownloadLogArchive, "/?source=local&key="+url.QueryEscape(name))
		if response.Code < 400 || strings.Contains(response.Body.String(), "secret-content") {
			t.Fatalf("name=%q status=%d", name, response.Code)
		}
	}
}

func TestLogArchiveRemoteErrorsDoNotExposeCredentials(t *testing.T) {
	for _, testcase := range []struct {
		err    error
		status int
	}{{logarchive.ErrNotFound, 404}, {errors.New("secret-token signed URL"), 502}} {
		h := archiveTestHandler(t, true, archiveReaderStub{open: func(context.Context, string) (io.ReadCloser, logarchive.Object, error) {
			return nil, logarchive.Object{}, testcase.err
		}})
		key := strings.Repeat("a", 64) + "/main.log.gz"
		response := archiveRequest(h.DownloadLogArchive, "/?source=s3&key="+url.QueryEscape(key))
		if response.Code != testcase.status || strings.Contains(response.Body.String(), "secret-token") {
			t.Fatalf("%d %s", response.Code, response.Body)
		}
	}
}

func TestDeleteLogsPreservesAllFilesWhenArchiving(t *testing.T) {
	h := archiveTestHandler(t, true, nil)
	name := filepath.Join(h.logDir, "main.log")
	if errWrite := os.WriteFile(name, []byte("history"), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	response := archiveRequest(h.DeleteLogs, "/")
	contents, errRead := os.ReadFile(name)
	if response.Code != 409 || errRead != nil || string(contents) != "history" {
		t.Fatalf("status=%d contents=%q err=%v", response.Code, contents, errRead)
	}
}

func TestLogArchivesRejectInvalidPagination(t *testing.T) {
	h := archiveTestHandler(t, true, nil)
	for _, query := range []string{"limit=0", "limit=1001", "limit=no", "cursor=garbage", "cursor=" + encodeLogArchiveCursor("local", "../main.log"), "cursor=" + encodeLogArchiveCursor("unexpected", "")} {
		response := archiveRequest(h.GetLogArchives, "/?"+query)
		if response.Code != 400 {
			t.Fatalf("query=%q status=%d", query, response.Code)
		}
	}
}

func TestDownloadLocalArchiveSupportsRangeWithoutDecompression(t *testing.T) {
	h := archiveTestHandler(t, false, nil)
	name := inspectionErrorName + ".gz"
	contents := []byte{0x1f, 0x8b, 0x08, 0x00, 0x12, 0x34}
	if errWrite := os.WriteFile(filepath.Join(h.logDir, name), contents, 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/?source=local&key="+url.QueryEscape(name), nil)
	c.Request.Header.Set("Range", "bytes=1-3")
	h.DownloadLogArchive(c)
	if response.Code != http.StatusPartialContent || !bytes.Equal(response.Body.Bytes(), contents[1:4]) || response.Header().Get("Content-Encoding") != "" {
		t.Fatalf("status=%d headers=%v body=%x", response.Code, response.Header(), response.Body.Bytes())
	}
}

func TestLogArchiveLocalPagesRemainAvailableDuringS3Failure(t *testing.T) {
	h := archiveTestHandler(t, true, archiveReaderStub{list: func(context.Context, string, int) (logarchive.ListResult, error) {
		return logarchive.ListResult{}, errors.New("secret-token upstream failure")
	}})
	if errWrite := os.WriteFile(filepath.Join(h.logDir, "main.log"), []byte("history"), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	response := archiveRequest(h.GetLogArchives, "/")
	if response.Code != 200 {
		t.Fatalf("local status=%d", response.Code)
	}
	response = archiveRequest(h.GetLogArchives, "/?cursor="+encodeLogArchiveCursor("s3", ""))
	if response.Code != 502 || strings.Contains(response.Body.String(), "secret-token") {
		t.Fatalf("remote status=%d body=%s", response.Code, response.Body)
	}
}

func TestLogArchivesIncludesRecoveredHistory(t *testing.T) {
	h := archiveTestHandler(t, false, nil)
	name := "recovered-response-body-123-550e8400-e29b-41d4-a716-446655440000.log"
	if errWrite := os.WriteFile(filepath.Join(h.logDir, name), []byte("partial response"), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	response := archiveRequest(h.GetLogArchives, "/")
	var page struct {
		Files []logArchiveEntry `json:"files"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Files) != 1 || page.Files[0].Kind != "recovered" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	response = archiveRequest(h.DownloadLogArchive, "/?source=local&key="+name)
	if response.Code != 200 || response.Body.String() != "partial response" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestDeleteLogsPreservesHistoryAfterArchivalEnabledWithStaleHandlerConfig(t *testing.T) {
	h := archiveTestHandler(t, false, nil)
	name := filepath.Join(h.logDir, "main.log")
	if errWrite := os.WriteFile(name, []byte("history"), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	// Invalid storage configuration starts no network worker but still enables
	// preservation. The handler deliberately retains its older configuration.
	if errConfigure := logarchive.Configure(h.logDir, config.LogArchiveConfig{Enabled: true}); errConfigure == nil {
		t.Fatal("expected invalid archive configuration")
	}
	t.Cleanup(func() {
		if errConfigure := logarchive.Configure(h.logDir, config.LogArchiveConfig{}); errConfigure != nil {
			t.Errorf("disable archival: %v", errConfigure)
		}
	})
	response := archiveRequest(h.DeleteLogs, "/")
	contents, errRead := os.ReadFile(name)
	if response.Code != http.StatusConflict || errRead != nil || string(contents) != "history" {
		t.Fatalf("status=%d contents=%q err=%v", response.Code, contents, errRead)
	}
}
