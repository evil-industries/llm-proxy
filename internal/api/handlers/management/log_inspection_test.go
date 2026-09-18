package management

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
)

const inspectionRequestName = "v1-chat-completions-2026-09-17T123456-a1b2c3d4.log"
const inspectionErrorName = "error-v1-responses-2026-09-17T123457-42.log"

func inspectionHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	return &Handler{cfg: &config.Config{}, logDir: dir}, dir
}

func writeInspectionFile(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	if errWrite := os.WriteFile(filepath.Join(dir, name), body, 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
}

func inspectRequest(handler gin.HandlerFunc, name, query string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/request-logs?"+query, nil)
	ctx.Params = gin.Params{{Key: "name", Value: name}}
	handler(ctx)
	return response
}

func TestRequestLogInspectionClassification(t *testing.T) {
	for _, test := range []struct{ name, kind string }{
		{inspectionRequestName, "request"}, {inspectionErrorName, "error"},
		{"root-2026-09-17T123456-123.log", "request"},
		{"v1-messages-2026-09-17T123456-123e4567-e89b-12d3-a456-426614174000.log", "request"},
		{"main.log", ""}, {"main.log.1", ""}, {"main-2026-09-17T12-34-56.log", ""},
		{"secret.log", ""}, {"error-secret.log", ""}, {"response-body-123.tmp", ""},
		{"v1-responses-2026-99-99T123456-42.log", ""},
		{"../" + inspectionRequestName, ""},
		{"unsafe\\" + inspectionRequestName, ""},
		{"unsafe\x01" + inspectionRequestName, ""},
		{"unsafe\n" + inspectionRequestName, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			kind, allowed := requestLogKind(test.name)
			if kind != test.kind || allowed != (test.kind != "") {
				t.Fatalf("kind = %q, allowed = %v, expected %q", kind, allowed, test.kind)
			}
		})
	}
}

func TestRequestLogInspectionListRegardlessOfLoggingSettings(t *testing.T) {
	handler, dir := inspectionHandler(t)
	writeInspectionFile(t, dir, inspectionRequestName, []byte("request text"))
	writeInspectionFile(t, dir, inspectionErrorName, []byte("error text"))
	writeInspectionFile(t, dir, "main.log", []byte("server log"))
	writeInspectionFile(t, dir, "main.log.1", []byte("rotated server log"))
	writeInspectionFile(t, dir, "secret.log", []byte("unrelated"))
	older := time.Unix(1000, 0)
	newer := time.Unix(2000, 0)
	if errTime := os.Chtimes(filepath.Join(dir, inspectionRequestName), older, older); errTime != nil {
		t.Fatal(errTime)
	}
	if errTime := os.Chtimes(filepath.Join(dir, inspectionErrorName), newer, newer); errTime != nil {
		t.Fatal(errTime)
	}
	for _, enabled := range []bool{false, true} {
		handler.cfg.RequestLog = enabled
		handler.cfg.LoggingToFile = enabled
		response := inspectRequest(handler.ListRequestLogs, "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("list status = %d: %s", response.Code, response.Body.String())
		}
		var body struct {
			Files []requestLogMetadata `json:"files"`
		}
		if errDecode := json.Unmarshal(response.Body.Bytes(), &body); errDecode != nil {
			t.Fatal(errDecode)
		}
		want := []requestLogMetadata{
			{Name: inspectionErrorName, Size: 10, Modified: 2000, Kind: "error"},
			{Name: inspectionRequestName, Size: 12, Modified: 1000, Kind: "request"},
		}
		if !reflect.DeepEqual(body.Files, want) {
			t.Fatalf("files = %+v, want %+v", body.Files, want)
		}
	}
}

func TestRequestLogInspectionPagesEntireLargeFile(t *testing.T) {
	handler, dir := inspectionHandler(t)
	content := strings.Repeat("large request body\n", 150000)
	writeInspectionFile(t, dir, inspectionRequestName, []byte(content))
	var result strings.Builder
	var offset int64
	pages := 0
	for {
		response := inspectRequest(handler.GetRequestLogPreview, inspectionRequestName, fmt.Sprintf("offset=%d&limit=%d", offset, requestLogPreviewMaximum))
		if response.Code != http.StatusOK {
			t.Fatalf("preview status = %d: %s", response.Code, response.Body.String())
		}
		var page requestLogPreview
		if errDecode := json.Unmarshal(response.Body.Bytes(), &page); errDecode != nil {
			t.Fatal(errDecode)
		}
		result.WriteString(page.Text)
		pages++
		if page.Name != inspectionRequestName || page.Size != int64(len(content)) || page.NextOffset <= offset {
			t.Fatalf("invalid page: %+v", page)
		}
		offset = page.NextOffset
		if !page.HasMore {
			break
		}
		if pages > 10 {
			t.Fatal("pagination did not finish")
		}
	}
	if pages < 3 || result.String() != content {
		t.Fatalf("whole-file pagination failed; pages=%d bytes=%d", pages, result.Len())
	}
	response := inspectRequest(handler.DownloadRequestLog, inspectionRequestName, "")
	if response.Code != http.StatusOK || response.Body.String() != content {
		t.Fatalf("full download truncated or failed: status=%d bytes=%d", response.Code, response.Body.Len())
	}
	if !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(response.Header().Get("Content-Disposition"), inspectionRequestName) {
		t.Fatal("download missing attachment filename")
	}
}

func TestRequestLogInspectionUTF8Pages(t *testing.T) {
	handler, dir := inspectionHandler(t)
	content := "aé世界🚀z"
	writeInspectionFile(t, dir, inspectionRequestName, []byte(content))
	for limit := 1; limit <= 7; limit++ {
		var joined strings.Builder
		var offset int64
		for joined.Len() < len(content) {
			response := inspectRequest(handler.GetRequestLogPreview, inspectionRequestName, fmt.Sprintf("offset=%d&limit=%d", offset, limit))
			if response.Code != http.StatusOK {
				t.Fatalf("UTF-8 preview failed: %s", response.Body.String())
			}
			var page requestLogPreview
			if errDecode := json.Unmarshal(response.Body.Bytes(), &page); errDecode != nil {
				t.Fatal(errDecode)
			}
			if !utf8.ValidString(page.Text) || strings.ContainsRune(page.Text, utf8.RuneError) || page.NextOffset <= offset {
				t.Fatalf("invalid UTF-8 page: %+v", page)
			}
			joined.WriteString(page.Text)
			offset = page.NextOffset
		}
		if joined.String() != content {
			t.Fatalf("limit %d changed text to %q", limit, joined.String())
		}
	}
	if response := inspectRequest(handler.GetRequestLogPreview, inspectionRequestName, "offset=2"); response.Code != http.StatusBadRequest {
		t.Fatalf("mid-character offset accepted: %d", response.Code)
	}
}

func TestRequestLogInspectionInvalidBytesAndEndOfFile(t *testing.T) {
	handler, dir := inspectionHandler(t)
	writeInspectionFile(t, dir, inspectionRequestName, []byte{'a', 0xff, 'b'})
	response := inspectRequest(handler.GetRequestLogPreview, inspectionRequestName, "")
	var page requestLogPreview
	if errDecode := json.Unmarshal(response.Body.Bytes(), &page); errDecode != nil {
		t.Fatal(errDecode)
	}
	if page.Text != "a\ufffdb" || page.NextOffset != 3 || page.HasMore {
		t.Fatalf("invalid binary-safe preview: %+v", page)
	}
	response = inspectRequest(handler.GetRequestLogPreview, inspectionRequestName, "offset=3")
	if errDecode := json.Unmarshal(response.Body.Bytes(), &page); errDecode != nil {
		t.Fatal(errDecode)
	}
	if response.Code != http.StatusOK || page.Text != "" || page.HasMore || page.NextOffset != 3 {
		t.Fatalf("invalid EOF preview: %+v", page)
	}
}

func TestRequestLogInspectionRejectsInvalidQueries(t *testing.T) {
	handler, dir := inspectionHandler(t)
	writeInspectionFile(t, dir, inspectionRequestName, []byte("content"))
	for _, query := range []string{"offset=-1", "offset=abc", "offset=", "offset=8", "offset=9223372036854775808", "limit=0", "limit=-1", "limit=1048577", "limit=abc", "limit=1&limit=2", "offset=0&offset=1", "all=true"} {
		t.Run(query, func(t *testing.T) {
			if response := inspectRequest(handler.GetRequestLogPreview, inspectionRequestName, query); response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRequestLogInspectionRejectsUnsafeFiles(t *testing.T) {
	handler, dir := inspectionHandler(t)
	writeInspectionFile(t, dir, "secret.log", []byte("secret-content"))
	for _, name := range []string{"../secret.log", `..\secret.log`, "main.log", "secret.log", inspectionRequestName, ""} {
		for _, operation := range []gin.HandlerFunc{handler.GetRequestLogPreview, handler.DownloadRequestLog} {
			response := inspectRequest(operation, name, "")
			if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
				t.Fatalf("unsafe file %q status=%d", name, response.Code)
			}
			if strings.Contains(response.Body.String(), "secret-content") || strings.Contains(response.Body.String(), dir) {
				t.Fatal("error disclosed file contents or filesystem paths")
			}
		}
	}
	if errMkdir := os.Mkdir(filepath.Join(dir, inspectionRequestName), 0o700); errMkdir != nil {
		t.Fatal(errMkdir)
	}
	if response := inspectRequest(handler.GetRequestLogPreview, inspectionRequestName, ""); response.Code != http.StatusNotFound {
		t.Fatal("directory accepted as log")
	}
}

func TestRequestLogInspectionRejectsSymlinks(t *testing.T) {
	handler, dir := inspectionHandler(t)
	outside := filepath.Join(t.TempDir(), "outside-secret")
	if errWrite := os.WriteFile(outside, []byte("outside-secret-content"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errLink := os.Symlink(outside, filepath.Join(dir, inspectionRequestName)); errLink != nil {
		t.Skipf("symlinks not available: %v", errLink)
	}
	for _, operation := range []gin.HandlerFunc{handler.GetRequestLogPreview, handler.DownloadRequestLog} {
		response := inspectRequest(operation, inspectionRequestName, "")
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "outside-secret") {
			t.Fatalf("symlink exposed: status=%d", response.Code)
		}
	}
	response := inspectRequest(handler.ListRequestLogs, "", "")
	if strings.Contains(response.Body.String(), inspectionRequestName) {
		t.Fatal("symlink listed as a request log")
	}
}

func TestRequestLogInspectionMissingDirectoryAndConfiguration(t *testing.T) {
	handler, dir := inspectionHandler(t)
	handler.logDir = filepath.Join(dir, "missing")
	response := inspectRequest(handler.ListRequestLogs, "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"files":[]`) {
		t.Fatalf("missing directory list: %d %s", response.Code, response.Body.String())
	}
	if response := inspectRequest(handler.GetRequestLogPreview, inspectionRequestName, ""); response.Code != http.StatusNotFound {
		t.Fatal("missing log did not return 404")
	}
	handler.cfg = nil
	for _, operation := range []gin.HandlerFunc{handler.ListRequestLogs, handler.GetRequestLogPreview, handler.DownloadRequestLog} {
		if response := inspectRequest(operation, inspectionRequestName, ""); response.Code != http.StatusServiceUnavailable {
			t.Fatalf("missing config status=%d", response.Code)
		}
	}
}

func TestRequestLogInspectionDisappearedFile(t *testing.T) {
	handler, dir := inspectionHandler(t)
	writeInspectionFile(t, dir, inspectionRequestName, []byte("request"))
	if response := inspectRequest(handler.ListRequestLogs, "", ""); response.Code != http.StatusOK {
		t.Fatal("initial list failed")
	}
	if errRemove := os.Remove(filepath.Join(dir, inspectionRequestName)); errRemove != nil {
		t.Fatal(errRemove)
	}
	for _, operation := range []gin.HandlerFunc{handler.GetRequestLogPreview, handler.DownloadRequestLog} {
		if response := inspectRequest(operation, inspectionRequestName, ""); response.Code != http.StatusNotFound {
			t.Fatalf("disappeared file status=%d", response.Code)
		}
	}
}

func TestRequestLogInspectionActualLoggerFilenames(t *testing.T) {
	for _, path := range []string{
		"/v1beta/models/gemini-2.5-pro(8192):generateContent",
		"/v1/models/model_+[]@,=$!&'",
		"/v1/models/model%2B#variant",
		"/v1/models/模型-é",
	} {
		t.Run(path, func(t *testing.T) {
			handler, dir := inspectionHandler(t)
			logger := logging.NewFileRequestLogger(true, dir, "", 10)
			if err := logger.LogRequest(path, http.MethodPost, nil, []byte("request fixture"), http.StatusOK, nil, []byte("response fixture"), nil, nil, nil, nil, nil, "request-id", time.Now(), time.Time{}); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("generated %d files, want1", len(entries))
			}
			name := entries[0].Name()
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			response := inspectRequest(handler.ListRequestLogs, "", "")
			var listed struct {
				Files []requestLogMetadata `json:"files"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || len(listed.Files) != 1 || listed.Files[0].Name != name || listed.Files[0].Kind != "request" {
				t.Fatalf("generated file excluded: %s: %s", name, response.Body)
			}
			response = inspectRequest(handler.GetRequestLogPreview, name, "")
			var preview requestLogPreview
			if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || preview.Text != string(raw) || preview.HasMore {
				t.Fatalf("generated file preview failed: %s", response.Body)
			}
			response = inspectRequest(handler.DownloadRequestLog, name, "")
			if response.Code != http.StatusOK || response.Body.String() != string(raw) {
				t.Fatalf("generated file download failed: %s", response.Body)
			}
		})
	}
}
