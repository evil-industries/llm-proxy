package middleware

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
)

type blockedFinalLog struct {
	testStreamingLogWriter
	entered chan struct{}
	release chan struct{}
}

func (w *blockedFinalLog) Close() error {
	close(w.entered)
	<-w.release
	return nil
}

func TestBackgroundFinalizationOwnsSnapshotAndCapturedFiles(t *testing.T) {
	setArchivePreservation(t, true)
	t.Cleanup(func() { setArchivePreservation(t, false) })
	logger := logging.NewFileRequestLogger(true, t.TempDir(), "", 0)
	source, errSource := logger.NewFileBodySource("timeline")
	if errSource != nil {
		t.Fatal(errSource)
	}
	if errAppend := source.AppendBytes([]byte("captured source")); errAppend != nil {
		t.Fatal(errAppend)
	}
	path := source.Paths()[0]
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	payload := []byte("original timeline")
	c.Set("API_WEBSOCKET_TIMELINE", payload)
	c.Set(logging.APIWebsocketTimelineSourceContextKey, source)
	drained := make(chan struct{})
	stream := &blockedFinalLog{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		close(stream.release)
		logging.WaitForRequestLogs()
		if _, errStat := os.Stat(path); !os.IsNotExist(errStat) {
			t.Errorf("completed background finalization did not clean its source: %v", errStat)
		}
	})
	wrapper := NewResponseWriterWrapper(c.Writer, logger, &RequestInfo{URL: "/v1/responses"})
	wrapper.isStreaming, wrapper.streamWriter, wrapper.streamDone = true, stream, drained
	if errFinalize := wrapper.finalizeAfterResponse(c); errFinalize != nil {
		t.Fatal(errFinalize)
	}
	// Simulate Gin recycling its context, a config reload, and outer middleware
	// writing after this middleware returns. The background task owns its inputs.
	copy(payload, bytes.Repeat([]byte("x"), len(payload)))
	c.Keys = nil
	logger.SetEnabled(false)
	_, _ = wrapper.WriteString("outer middleware")
	close(drained)
	<-stream.entered
	if got := string(stream.apiWebsocketTimeline); got != "original timeline\n\ncaptured source" {
		t.Fatalf("snapshot changed during finalization: %q", got)
	}
	if _, errStat := os.Stat(path); errStat != nil {
		t.Fatalf("source cleaned before finalization finished: %v", errStat)
	}
}

func TestBackgroundFinalizationPreservesSourcesAfterFailure(t *testing.T) {
	setArchivePreservation(t, true)
	t.Cleanup(func() { setArchivePreservation(t, false) })
	logger := logging.NewFileRequestLogger(true, t.TempDir(), "", 0)
	source, errSource := logger.NewFileBodySource("failed")
	if errSource != nil {
		t.Fatal(errSource)
	}
	t.Cleanup(func() { _ = source.Cleanup() })
	if errAppend := source.AppendBytes([]byte("preserved bytes")); errAppend != nil {
		t.Fatal(errAppend)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set(logging.APIResponseSourceContextKey, source)
	wrapper := NewResponseWriterWrapper(c.Writer, logger, &RequestInfo{URL: "/v1/responses"})
	wrapper.isStreaming = true
	wrapper.streamWriter = &archiveFailureStream{err: errors.New("publication failed")}
	if errFinalize := wrapper.finalizeAfterResponse(c); errFinalize != nil {
		t.Fatal(errFinalize)
	}
	logging.WaitForRequestLogs()
	data, errRead := os.ReadFile(source.Paths()[0])
	if errRead != nil || string(data) != "preserved bytes" {
		t.Fatalf("failed background publication discarded capture: %q, %v", data, errRead)
	}
}
