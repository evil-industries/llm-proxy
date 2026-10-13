package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/home"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
)

type homeRequestLogClient interface {
	HeartbeatOK() bool
	RPushRequestLog(ctx context.Context, payload []byte) error
}

var currentHomeRequestLogClient = func() homeRequestLogClient {
	return home.Current()
}

type homeRequestLogPayload struct {
	Headers    map[string][]string `json:"headers,omitempty"`
	RequestID  string              `json:"request_id,omitempty"`
	RequestLog string              `json:"request_log,omitempty"`
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if values == nil {
			out[key] = nil
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (l *FileRequestLogger) forwardRequestLogToHome(ctx context.Context, headers map[string][]string, requestID string, logText string) error {
	if l == nil || !l.homeEnabled {
		return nil
	}
	client := currentHomeRequestLogClient()
	if client == nil || !client.HeartbeatOK() {
		return nil
	}
	payload := homeRequestLogPayload{
		Headers:    cloneHeaders(headers),
		RequestID:  strings.TrimSpace(requestID),
		RequestLog: logText,
	}
	raw, errMarshal := json.Marshal(&payload)
	if errMarshal != nil {
		return errMarshal
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return client.RPushRequestLog(ctx, raw)
}

// SetHomeEnabled toggles home request-log forwarding.
// Logs are forwarded via Redis RESP. With archival enabled, a local copy is
// published first so Home availability cannot prevent S3 archival.
func (l *FileRequestLogger) SetHomeEnabled(enabled bool) {
	if l == nil {
		return
	}
	l.homeEnabled = enabled
}

type homeStreamingLogWriter struct {
	// Keep the original directory owner when archival is enabled mid-request.
	fileLogger      FileRequestLogger
	fileWriter      *FileStreamingLogWriter
	captureEnabled  bool
	preserveLatched atomic.Bool

	url       string
	method    string
	timestamp time.Time

	requestHeaders map[string][]string
	requestBody    []byte

	chunkChan chan []byte
	doneChan  chan struct{}

	responseStatus   int
	statusWritten    bool
	responseHeaders  map[string][]string
	responseBody     bytes.Buffer
	apiRequest       []byte
	apiResponse      []byte
	apiWebsocketTime []byte
	requestID        string
	apiResponseTS    time.Time
	firstChunkTS     time.Time
}

func newHomeStreamingLogWriter(logger *FileRequestLogger, captureEnabled bool, url, method string, headers map[string][]string, body []byte, requestID string) *homeStreamingLogWriter {
	requestHeaders := make(map[string][]string, len(headers))
	for key, values := range headers {
		headerValues := make([]string, len(values))
		copy(headerValues, values)
		requestHeaders[key] = headerValues
	}

	writer := &homeStreamingLogWriter{
		fileLogger:     *logger,
		captureEnabled: captureEnabled,
		url:            url,
		method:         method,
		timestamp:      time.Now(),
		requestHeaders: requestHeaders,
		requestBody:    append([]byte(nil), body...),
		requestID:      strings.TrimSpace(requestID),
		chunkChan:      make(chan []byte, 100),
		doneChan:       make(chan struct{}),
	}

	go writer.asyncWriter()
	return writer
}

// PreserveLogs and PreservingLogs share the request's latched policy with the
// middleware without touching the response buffer owned by the queue consumer.
func (w *homeStreamingLogWriter) PreserveLogs() { w.preserveLatched.Store(true) }

func (w *homeStreamingLogWriter) PreservingLogs() bool {
	if w.preserveLatched.Load() || logarchive.Enabled() {
		w.PreserveLogs()
		return true
	}
	return false
}

func (w *homeStreamingLogWriter) stopHomeChunks() {
	if w.chunkChan != nil {
		close(w.chunkChan)
		<-w.doneChan
		w.chunkChan = nil
	}
}

// activateFileWriter is called only by the serialized chunk producer or Close
// after that producer has stopped. Existing captured chunks move into the spool
// before new chunks, retaining their order and the original request metadata.
func (w *homeStreamingLogWriter) activateFileWriter() error {
	if w.fileWriter != nil {
		return nil
	}
	writer, errCreate := w.fileLogger.newFileStreamingLogWriter(w.url, w.method, w.requestHeaders, w.requestBody, w.requestID)
	if errCreate != nil {
		return fmt.Errorf("failed to activate request log archival: %w", errCreate)
	}
	writer.PreserveLogs()
	writer.timestamp = w.timestamp
	w.stopHomeChunks()
	if w.responseBody.Len() > 0 {
		writer.WriteChunkAsync(w.responseBody.Bytes())
	}
	w.responseBody = bytes.Buffer{}
	w.requestBody = nil
	w.fileWriter = writer
	return nil
}

func (w *homeStreamingLogWriter) asyncWriter() {
	defer close(w.doneChan)
	for chunk := range w.chunkChan {
		if len(chunk) == 0 {
			continue
		}
		_, _ = w.responseBody.Write(chunk)
	}
}

func (w *homeStreamingLogWriter) WriteChunkAsync(chunk []byte) {
	if w == nil || len(chunk) == 0 {
		return
	}
	if w.PreservingLogs() {
		if errActivate := w.activateFileWriter(); errActivate == nil {
			w.fileWriter.WriteChunkAsync(chunk)
			return
		}
		// Keep the captured bytes for another activation attempt and surface a
		// persistent storage failure from Close; never drop a preserved chunk.
		if w.chunkChan != nil {
			w.chunkChan <- bytes.Clone(chunk)
		}
		return
	}
	if !w.captureEnabled || w.chunkChan == nil {
		return
	}
	select {
	case w.chunkChan <- append([]byte(nil), chunk...):
	default:
	}
}

func (w *homeStreamingLogWriter) WriteStatus(status int, headers map[string][]string) error {
	if w == nil || status == 0 {
		return nil
	}
	w.responseStatus = status
	w.statusWritten = true
	if headers != nil {
		w.responseHeaders = make(map[string][]string, len(headers))
		for key, values := range headers {
			copied := make([]string, len(values))
			copy(copied, values)
			w.responseHeaders[key] = copied
		}
	}
	return nil
}

func (w *homeStreamingLogWriter) WriteAPIRequest(apiRequest []byte) error {
	if w == nil || len(apiRequest) == 0 {
		return nil
	}
	w.apiRequest = bytes.Clone(apiRequest)
	return nil
}

func (w *homeStreamingLogWriter) WriteAPIResponse(apiResponse []byte) error {
	if w == nil || len(apiResponse) == 0 {
		return nil
	}
	w.apiResponse = bytes.Clone(apiResponse)
	return nil
}

func (w *homeStreamingLogWriter) WriteAPIWebsocketTimeline(apiWebsocketTimeline []byte) error {
	if w == nil || len(apiWebsocketTimeline) == 0 {
		return nil
	}
	w.apiWebsocketTime = bytes.Clone(apiWebsocketTimeline)
	return nil
}

func (w *homeStreamingLogWriter) SetFirstChunkTimestamp(timestamp time.Time) {
	if w == nil {
		return
	}
	if !timestamp.IsZero() {
		w.firstChunkTS = timestamp
		w.apiResponseTS = timestamp
	}
}

func (w *homeStreamingLogWriter) Close() error {
	if w == nil {
		return nil
	}

	w.stopHomeChunks()
	if w.PreservingLogs() {
		if errActivate := w.activateFileWriter(); errActivate != nil {
			return errActivate
		}
		writer := w.fileWriter
		writer.responseStatus = w.responseStatus
		writer.statusWritten = w.statusWritten
		writer.responseHeaders = w.responseHeaders
		writer.apiRequest = w.apiRequest
		writer.apiResponse = w.apiResponse
		writer.apiWebsocketTimeline = w.apiWebsocketTime
		writer.apiResponseTimestamp = w.apiResponseTS
		return writer.Close()
	}
	if !w.captureEnabled {
		return nil
	}

	client := currentHomeRequestLogClient()
	if client == nil || !client.HeartbeatOK() {
		return nil
	}

	responsePayload := w.responseBody.Bytes()

	var buf bytes.Buffer
	upstreamTransport := inferUpstreamTransport(w.apiRequest, nil, w.apiResponse, nil, w.apiWebsocketTime, nil, nil)
	if errWrite := writeRequestInfoWithBody(&buf, w.url, w.method, w.requestHeaders, w.requestBody, "", w.timestamp, "http", upstreamTransport, true); errWrite != nil {
		return errWrite
	}
	if errWrite := writeAPISection(&buf, "=== API WEBSOCKET TIMELINE ===\n", "=== API WEBSOCKET TIMELINE", w.apiWebsocketTime, time.Time{}); errWrite != nil {
		return errWrite
	}
	if errWrite := writeAPISection(&buf, "=== API REQUEST ===\n", "=== API REQUEST", w.apiRequest, time.Time{}); errWrite != nil {
		return errWrite
	}
	if errWrite := writeAPISection(&buf, "=== API RESPONSE ===\n", "=== API RESPONSE", w.apiResponse, w.apiResponseTS); errWrite != nil {
		return errWrite
	}
	if errWrite := writeResponseSection(&buf, w.responseStatus, w.statusWritten, w.responseHeaders, bytes.NewReader(responsePayload), nil, false); errWrite != nil {
		return errWrite
	}

	payload := homeRequestLogPayload{
		Headers:    cloneHeaders(w.requestHeaders),
		RequestID:  w.requestID,
		RequestLog: buf.String(),
	}
	raw, errMarshal := json.Marshal(&payload)
	if errMarshal != nil {
		return errMarshal
	}
	return client.RPushRequestLog(context.Background(), raw)
}
