package logging

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
)

func TestHomeStreamingHotEnablePublishesLocalArchive(t *testing.T) {
	original := currentHomeRequestLogClient
	t.Cleanup(func() { currentHomeRequestLogClient = original })
	for _, healthy := range []bool{true, false} {
		name := "healthy"
		if !healthy {
			name = "unhealthy"
		}
		t.Run(name, func(t *testing.T) {
			if errConfigure := logarchive.Configure("", config.LogArchiveConfig{}); errConfigure != nil {
				t.Fatal(errConfigure)
			}
			stub := &stubHomeRequestLogClient{heartbeatOK: healthy}
			currentHomeRequestLogClient = func() homeRequestLogClient { return stub }
			dir := t.TempDir()
			logger := NewFileRequestLogger(true, dir, "", 0)
			logger.SetHomeEnabled(true)
			writer, errStart := logger.LogStreamingRequest("/v1/responses", http.MethodPost, nil, []byte("original request body"), "hot-home")
			if errStart != nil {
				t.Fatal(errStart)
			}
			if errStatus := writer.WriteStatus(http.StatusOK, map[string][]string{"Content-Type": {"text/event-stream"}}); errStatus != nil {
				t.Fatal(errStatus)
			}
			writer.WriteChunkAsync([]byte("before enable\n"))
			entries, errRead := os.ReadDir(dir)
			if errRead != nil || len(entries) != 0 {
				t.Fatalf("Home logging created local files before enable: %v, %v", entries, errRead)
			}
			enableArchivePreservation(t)
			chunk := []byte("retained after enable\n")
			for range 300 {
				writer.WriteChunkAsync(chunk)
			}
			// A later disable must not discard the already activated local spool.
			if errConfigure := logarchive.Configure("", config.LogArchiveConfig{}); errConfigure != nil {
				t.Fatal(errConfigure)
			}
			for range 300 {
				writer.WriteChunkAsync(chunk)
			}
			if errRequest := writer.WriteAPIRequest([]byte("upstream request detail")); errRequest != nil {
				t.Fatal(errRequest)
			}
			if errResponse := writer.WriteAPIResponse([]byte("upstream response detail")); errResponse != nil {
				t.Fatal(errResponse)
			}
			if errClose := writer.Close(); errClose != nil {
				t.Fatal(errClose)
			}
			entries, errRead = os.ReadDir(dir)
			if errRead != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-hot-home.log") {
				t.Fatalf("published local logs = %v, %v", entries, errRead)
			}
			content, errRead := os.ReadFile(filepath.Join(dir, entries[0].Name()))
			if errRead != nil {
				t.Fatal(errRead)
			}
			if got := bytes.Count(content, chunk); got != 600 {
				t.Fatalf("retained chunks = %d, want 600", got)
			}
			for _, expected := range []string{"original request body", "upstream request detail", "upstream response detail", "text/event-stream"} {
				if !bytes.Contains(content, []byte(expected)) {
					t.Fatalf("published log omitted %q", expected)
				}
			}
			if healthy && !bytes.Contains(content, []byte("before enable")) {
				t.Fatal("activation lost previously captured Home chunks")
			}
			wantForwarded := 0
			if healthy {
				wantForwarded = 1
			}
			if len(stub.pushed) != wantForwarded {
				t.Fatalf("Home records = %d, want %d", len(stub.pushed), wantForwarded)
			}
		})
	}
}

func TestHomeStreamingPreservePropagationActivatesAtClose(t *testing.T) {
	original := currentHomeRequestLogClient
	t.Cleanup(func() { currentHomeRequestLogClient = original })
	currentHomeRequestLogClient = func() homeRequestLogClient { return nil }
	if errConfigure := logarchive.Configure("", config.LogArchiveConfig{}); errConfigure != nil {
		t.Fatal(errConfigure)
	}
	dir := t.TempDir()
	logger := NewFileRequestLogger(true, dir, "", 0)
	logger.SetHomeEnabled(true)
	writer, errStart := logger.LogStreamingRequest("/v1/responses", http.MethodPost, nil, []byte("retained request"), "close-enable")
	if errStart != nil {
		t.Fatal(errStart)
	}
	// Middleware may observe the enablement while no more chunks arrive. The
	// latched policy must still activate local publication after global disable.
	writer.(interface{ PreserveLogs() }).PreserveLogs()
	if errClose := writer.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	entries, errRead := os.ReadDir(dir)
	if errRead != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-close-enable.log") {
		t.Fatalf("published local logs = %v, %v", entries, errRead)
	}
}

func TestHomeStreamingWithoutArchivalLeavesNoLocalFiles(t *testing.T) {
	original := currentHomeRequestLogClient
	t.Cleanup(func() { currentHomeRequestLogClient = original })
	currentHomeRequestLogClient = func() homeRequestLogClient { return nil }
	if errConfigure := logarchive.Configure("", config.LogArchiveConfig{}); errConfigure != nil {
		t.Fatal(errConfigure)
	}
	dir := t.TempDir()
	logger := NewFileRequestLogger(true, dir, "", 0)
	logger.SetHomeEnabled(true)
	writer, errStart := logger.LogStreamingRequest("/v1/responses", http.MethodPost, nil, nil, "unlogged-home")
	if errStart != nil {
		t.Fatal(errStart)
	}
	writer.WriteChunkAsync([]byte("not captured while Home is unavailable"))
	if errClose := writer.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	entries, errRead := os.ReadDir(dir)
	if errRead != nil || len(entries) != 0 {
		t.Fatalf("legacy unavailable Home created local logs: %v, %v", entries, errRead)
	}
}

func TestHomeStreamingArchiveActivationFailureKeepsCapturedBytes(t *testing.T) {
	original := currentHomeRequestLogClient
	t.Cleanup(func() { currentHomeRequestLogClient = original })
	currentHomeRequestLogClient = func() homeRequestLogClient { return nil }
	if errConfigure := logarchive.Configure("", config.LogArchiveConfig{}); errConfigure != nil {
		t.Fatal(errConfigure)
	}
	dir := filepath.Join(t.TempDir(), "unwritable-logs")
	if errWrite := os.WriteFile(dir, []byte("not a directory"), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	logger := NewFileRequestLogger(true, dir, "", 0)
	logger.SetHomeEnabled(true)
	writer, errStart := logger.LogStreamingRequest("/v1/responses", http.MethodPost, nil, []byte("request"), "activation-fails")
	if errStart != nil {
		t.Fatal(errStart)
	}
	writer.(interface{ PreserveLogs() }).PreserveLogs()
	writer.WriteChunkAsync([]byte("preserved while activation fails"))
	if errClose := writer.Close(); errClose == nil {
		t.Fatal("expected local spool activation failure")
	}
	homeWriter := writer.(*homeStreamingLogWriter)
	if got := homeWriter.responseBody.String(); got != "preserved while activation fails" {
		t.Fatalf("buffer after activation failure = %q", got)
	}
	select {
	case <-homeWriter.doneChan:
	default:
		t.Fatal("Home response consumer was not stopped after failed Close")
	}
}
