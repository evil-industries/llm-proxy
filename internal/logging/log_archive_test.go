package logging

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
