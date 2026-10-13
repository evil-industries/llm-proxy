package logging

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
)

func enableArchivePreservation(t *testing.T) {
	t.Helper()
	// Invalid credentials/configuration must still disable destructive retention.
	if err := logarchive.Configure(t.TempDir(), config.LogArchiveConfig{Enabled: true}); err == nil {
		t.Fatal("expected invalid configuration")
	}
	t.Cleanup(func() {
		if err := logarchive.Configure("", config.LogArchiveConfig{}); err != nil {
			t.Error(err)
		}
	})
}
func TestArchiveDisablesDestructiveRetention(t *testing.T) {
	enableArchivePreservation(t)
	dir := t.TempDir()
	for _, name := range []string{"error-one.log", "error-two.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("retained log"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	logger := NewFileRequestLogger(false, dir, "", 1)
	if err := logger.cleanupOldErrorLogs(); err != nil {
		t.Fatal(err)
	}
	removed, err := enforceLogDirSizeLimit(dir, 1, "")
	if err != nil || removed != 0 {
		t.Fatalf("cleaner removed %d: %v", removed, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("files = %d, %v", len(files), err)
	}
}
func TestArchiveFailedPublicationPreservesCapturedSources(t *testing.T) {
	enableArchivePreservation(t)
	dir := t.TempDir()
	logger := NewFileRequestLogger(true, dir, "", 10)
	source, err := logger.NewFileBodySource("api-request")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.AppendPart([]byte("original upstream detail")); err != nil {
		t.Fatal(err)
	}
	paths := source.Paths()
	// An overlong final filename fails rename after the pending log has been written.
	err = logger.LogRequestWithOptionsAndAllSources("/v1/responses", "POST", nil, []byte("request body"), 200, nil, []byte("response"), nil, nil, nil, source, nil, nil, nil, nil, nil, false, strings.Repeat("x", 300), time.Now(), time.Time{})
	if err == nil {
		t.Fatal("expected publication failure")
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("source discarded on failure: %v", err)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	pending, body := false, false
	for _, file := range files {
		pending = pending || strings.HasSuffix(file.Name(), ".pending")
		body = body || strings.HasPrefix(file.Name(), "request-body-")
	}
	if !pending || !body {
		t.Fatalf("capture missing after failure: pending=%v body=%v", pending, body)
	}
}
func TestArchiveStreamingPreservesEveryChunk(t *testing.T) {
	enableArchivePreservation(t)
	dir := t.TempDir()
	logger := NewFileRequestLogger(true, dir, "", 10)
	writer, err := logger.LogStreamingRequest("/v1/responses", "POST", nil, []byte("request"), "stream-id")
	if err != nil {
		t.Fatal(err)
	}
	chunk := []byte("unique response chunk\n")
	for range 1000 {
		writer.WriteChunkAsync(chunk)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), "-stream-id.log") {
		t.Fatalf("unexpected published files: %v", files)
	}
	raw, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if count := bytes.Count(raw, chunk); count != 1000 {
		t.Fatalf("retained chunks = %d", count)
	}
}

type publicationCheckingHomeClient struct{ check func() }

func (c *publicationCheckingHomeClient) HeartbeatOK() bool { return true }
func (c *publicationCheckingHomeClient) RPushRequestLog(context.Context, []byte) error {
	c.check()
	return nil
}

func TestArchivePublishesBeforeHomeForwarding(t *testing.T) {
	enableArchivePreservation(t)
	original := currentHomeRequestLogClient
	t.Cleanup(func() { currentHomeRequestLogClient = original })
	for _, streaming := range []bool{false, true} {
		name := "nonstreaming"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			forwarded := false
			client := &publicationCheckingHomeClient{check: func() {
				completed, err := filepath.Glob(filepath.Join(dir, "*.log"))
				if err != nil || len(completed) != 1 {
					t.Fatalf("Home invoked before publication: %v %v", completed, err)
				}
				forwarded = true
			}}
			currentHomeRequestLogClient = func() homeRequestLogClient { return client }
			logger := NewFileRequestLogger(true, dir, "", 10)
			logger.SetHomeEnabled(true)
			if streaming {
				writer, err := logger.LogStreamingRequest("/v1/responses", "POST", nil, nil, "id")
				if err != nil {
					t.Fatal(err)
				}
				writer.WriteChunkAsync([]byte("response"))
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := logger.LogRequest("/v1/responses", "POST", nil, nil, 200, nil, []byte("response"), nil, nil, nil, nil, nil, "id", time.Now(), time.Time{}); err != nil {
					t.Fatal(err)
				}
			}
			if !forwarded {
				t.Fatal("Home forwarding skipped")
			}
		})
	}
}

func TestDisabledArchiveFailedPublicationCleansStaging(t *testing.T) {
	if err := logarchive.Configure("", config.LogArchiveConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, streaming := range []bool{false, true} {
		name := "nonstreaming"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			logger := NewFileRequestLogger(true, dir, "", 10)
			source, err := logger.NewFileBodySource("detail")
			if err != nil {
				t.Fatal(err)
			}
			if err := source.AppendPart([]byte("detail payload")); err != nil {
				t.Fatal(err)
			}
			if streaming {
				writer, err := logger.LogStreamingRequest("/v1/responses", "POST", nil, []byte("request"), strings.Repeat("x", 300))
				if err != nil {
					t.Fatal(err)
				}
				fileWriter := writer.(*FileStreamingLogWriter)
				fileWriter.apiRequestSource = source
				writer.WriteChunkAsync([]byte("response"))
				if err := writer.Close(); err == nil {
					t.Fatal("expected publication failure")
				}
			} else {
				err = logger.LogRequestWithOptionsAndAllSources("/v1/responses", "POST", nil, []byte("request"), 200, nil, []byte("response"), nil, nil, nil, source, nil, nil, nil, nil, nil, false, strings.Repeat("x", 300), time.Now(), time.Time{})
				if err == nil {
					t.Fatal("expected publication failure")
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("disabled archival leaked staging files: %v", entries)
			}
		})
	}
}

func TestArchiveFailedPublicationUploadsWithoutRestart(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "nonstreaming"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			invalid := config.LogArchiveConfig{Enabled: true}
			if err := logarchive.Configure(dir, invalid); err == nil {
				t.Fatal("expected invalid bucket")
			}
			t.Cleanup(func() {
				logarchive.Stop()
				if err := logarchive.Configure("", config.LogArchiveConfig{}); err != nil {
					t.Error(err)
				}
			})
			logger := NewFileRequestLogger(true, dir, "", 10)
			marker := []byte("full response recovered in the same service session")
			if streaming {
				writer, err := logger.LogStreamingRequest("/v1/responses", "POST", nil, []byte("request body"), strings.Repeat("x", 300))
				if err != nil {
					t.Fatal(err)
				}
				writer.WriteChunkAsync(marker)
				if err := writer.Close(); err == nil {
					t.Fatal("expected final publication failure")
				}
			} else {
				if err := logger.LogRequest("/v1/responses", "POST", nil, []byte("request body"), 200, nil, marker, nil, nil, nil, nil, nil, strings.Repeat("x", 300), time.Now(), time.Time{}); err == nil {
					t.Fatal("expected final publication failure")
				}
			}
			var mu sync.Mutex
			objects := map[string][]byte{}
			verifiedDownload := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("ETag", `"test-etag"`)
				switch r.Method {
				case http.MethodPut:
					var body io.Reader = r.Body
					if strings.Contains(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-AWS4-HMAC-SHA256") {
						body = httputil.NewChunkedReader(body)
					}
					raw, err := io.ReadAll(body)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					objects[r.URL.Path] = raw
				case http.MethodHead, http.MethodGet:
					raw, ok := objects[r.URL.Path]
					if !ok {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
					w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
					if r.Method == http.MethodGet {
						if _, err := w.Write(raw); err != nil {
							t.Error(err)
							return
						}
						reader, err := gzip.NewReader(bytes.NewReader(raw))
						if err != nil {
							t.Error(err)
							return
						}
						restored, err := io.ReadAll(reader)
						if err != nil {
							t.Error(err)
						}
						if err := reader.Close(); err != nil {
							t.Error(err)
						}
						if bytes.Contains(restored, marker) && bytes.Contains(restored, []byte("=== REQUEST INFO ===")) {
							select {
							case verifiedDownload <- struct{}{}:
							default:
							}
						}
					}
				default:
					t.Errorf("unexpected S3 method %s", r.Method)
					w.WriteHeader(400)
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(logarchive.Stop)
			cfg := config.LogArchiveConfig{Enabled: true, Endpoint: server.URL, Bucket: "archive-test", Region: "us-east-1", AccessKeyID: "test-access", SecretAccessKey: "test-secret", PathStyle: true}
			// Reconfigure the same service after its destination becomes usable. Its
			// initial scan must consume explicitly queued failures, without a restart.
			if err := logarchive.Configure(dir, cfg); err != nil {
				t.Fatal(err)
			}
			select {
			case <-verifiedDownload:
			case <-time.After(10 * time.Second):
				t.Fatal("closed failed request log was not uploaded for verification")
			}
		})
	}
}

func TestFileBodySourceCreatedAfterRetirementKeepsLoggerOwner(t *testing.T) {
	logarchive.Stop()
	t.Cleanup(func() {
		logarchive.Stop()
		if err := logarchive.Configure("", config.LogArchiveConfig{}); err != nil {
			t.Error(err)
		}
	})
	oldLogger := NewFileRequestLogger(true, t.TempDir(), "", 10)
	originalOwner := oldLogger.archiveDirectory
	logarchive.Stop()
	newLogger := NewFileRequestLogger(true, t.TempDir(), "", 10)
	source, err := oldLogger.NewFileBodySource("late-source")
	if err != nil {
		t.Fatal(err)
	}
	if source.archiveDirectory != originalOwner || source.archiveDirectory == newLogger.archiveDirectory {
		t.Fatal("late old-service source acquired new-service ownership")
	}
}
