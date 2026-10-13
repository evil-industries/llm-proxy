package middleware

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
)

type archiveFailureLogger struct {
	testRequestLogger
	err error
}

func (l *archiveFailureLogger) LogRequest(string, string, map[string][]string, []byte, int, map[string][]string, []byte, []byte, []byte, []byte, []byte, []*interfaces.ErrorMessage, string, time.Time, time.Time) error {
	return l.err
}

type archiveFailureStream struct {
	testStreamingLogWriter
	err    error
	chunks []string
}

func (w *archiveFailureStream) WriteChunkAsync(chunk []byte) {
	w.chunks = append(w.chunks, string(chunk))
}

func (w *archiveFailureStream) Close() error {
	w.closed = true
	return w.err
}

func TestFinalizePreservesSourcesOnlyForFailedArchivePublication(t *testing.T) {
	failure := errors.New("injected log publication failure")
	for _, tc := range []struct {
		name      string
		preserve  bool
		streaming bool
		enabled   bool
		err       error
	}{
		{name: "failed ordinary archive", preserve: true, enabled: true, err: failure},
		{name: "failed streaming archive", preserve: true, streaming: true, enabled: true, err: failure},
		{name: "successful ordinary archive", preserve: true, enabled: true},
		{name: "successful streaming archive", preserve: true, streaming: true, enabled: true},
		{name: "failed ordinary legacy log", enabled: true, err: failure},
		{name: "failed streaming legacy log", streaming: true, enabled: true, err: failure},
		{name: "intentionally uncaptured request", preserve: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			var paths []string
			for _, key := range []string{
				logging.APIRequestSourceContextKey, logging.APIResponseSourceContextKey,
				logging.WebsocketTimelineSourceContextKey, logging.APIWebsocketTimelineSourceContextKey,
			} {
				source, errSource := logging.NewFileBodySourceInDir(t.TempDir(), "archive-test")
				if errSource != nil {
					t.Fatal(errSource)
				}
				if errAppend := source.AppendBytes([]byte("complete captured source")); errAppend != nil {
					t.Fatal(errAppend)
				}
				paths = append(paths, source.Paths()...)
				c.Set(key, source)
			}
			deferredSource, errSource := logging.NewFileBodySourceInDir(t.TempDir(), "request-body")
			if errSource != nil {
				t.Fatal(errSource)
			}
			deferredFile, errPart := deferredSource.CreatePart("body")
			if errPart != nil {
				t.Fatal(errPart)
			}
			if _, errWrite := deferredFile.WriteString("complete captured source"); errWrite != nil {
				t.Fatal(errWrite)
			}
			paths = append(paths, deferredSource.Paths()...)
			capture := &deferredRequestBodyCapture{source: deferredSource, file: deferredFile}
			stream := &archiveFailureStream{err: tc.err}
			wrapper := &ResponseWriterWrapper{
				ResponseWriter: c.Writer,
				logger:         &archiveFailureLogger{testRequestLogger: testRequestLogger{enabled: tc.enabled}, err: tc.err},
				requestInfo:    &RequestInfo{URL: "/v1/responses", deferredBodyCapture: capture},
				preserveLogs:   tc.preserve,
				isStreaming:    tc.streaming,
				body:           &bytes.Buffer{},
			}
			if tc.streaming {
				wrapper.streamWriter = stream
			}
			if errFinalize := wrapper.Finalize(c); !errors.Is(errFinalize, tc.err) {
				t.Fatalf("Finalize error = %v, want %v", errFinalize, tc.err)
			}
			if tc.streaming && !stream.closed {
				t.Fatal("stream writer was not closed")
			}
			if capture.file != nil {
				t.Fatal("deferred capture handle was not closed")
			}
			wantPreserved := tc.preserve && tc.err != nil
			for _, path := range paths {
				payload, errRead := os.ReadFile(path)
				if wantPreserved {
					if errRead != nil || string(payload) != "complete captured source" {
						t.Fatalf("preserved source %q = %q, %v", path, payload, errRead)
					}
				} else if !os.IsNotExist(errRead) {
					t.Fatalf("source %q remains after cleanup: %v", path, errRead)
				}
			}
		})
	}
}

func TestArchiveStreamingQueuePreservesChunksWhenFull(t *testing.T) {
	for _, tc := range []struct {
		name        string
		preserve    bool
		writeString bool
	}{
		{name: "archived bytes", preserve: true},
		{name: "archived string", preserve: true, writeString: true},
		{name: "legacy bytes"},
		{name: "legacy string", writeString: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				chunks := make(chan []byte, 1)
				chunks <- []byte("queued chunk")
				wrapper := &ResponseWriterWrapper{ResponseWriter: c.Writer, isStreaming: true, chunkChannel: chunks, preserveLogs: tc.preserve}
				done := make(chan struct{})
				go func() {
					defer close(done)
					if tc.writeString {
						_, _ = wrapper.WriteString("next chunk")
					} else {
						_, _ = wrapper.Write([]byte("next chunk"))
					}
				}()
				synctest.Wait()
				if recorder.Body.String() != "next chunk" {
					t.Fatal("client response was not written before logging backpressure")
				}
				select {
				case <-done:
					if tc.preserve {
						t.Fatal("archival dropped a chunk instead of waiting for queue capacity")
					}
				default:
					if !tc.preserve {
						t.Fatal("legacy logging blocked the request")
					}
				}
				<-chunks
				<-done
				if tc.preserve {
					if got := string(<-chunks); got != "next chunk" {
						t.Fatalf("captured chunk = %q", got)
					}
				} else if len(chunks) != 0 {
					t.Fatal("legacy full-queue policy changed")
				}
			})
		})
	}
}

func TestFinalizeDrainsStartedStreamAfterLoggerDisabled(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	failure := errors.New("injected close failure")
	stream := &archiveFailureStream{err: failure}
	chunks := make(chan []byte, 2)
	chunks <- []byte("first")
	chunks <- []byte("second")
	done := make(chan struct{})
	wrapper := &ResponseWriterWrapper{
		ResponseWriter: c.Writer,
		logger:         &testRequestLogger{enabled: false},
		requestInfo:    &RequestInfo{URL: "/v1/responses"},
		preserveLogs:   true,
		isStreaming:    true,
		streamWriter:   stream,
		chunkChannel:   chunks,
		streamDone:     done,
	}
	go wrapper.processStreamingChunks(done, chunks, stream)
	if errFinalize := wrapper.Finalize(c); !errors.Is(errFinalize, failure) {
		t.Fatalf("Finalize error = %v, want %v", errFinalize, failure)
	}
	if !stream.closed || len(stream.chunks) != 2 || stream.chunks[0] != "first" || stream.chunks[1] != "second" {
		t.Fatalf("stream closed=%v, drained chunks=%v", stream.closed, stream.chunks)
	}
}
