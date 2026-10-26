package middleware

import (
	"bytes"
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	log "github.com/sirupsen/logrus"
)

// finalizeAfterResponse transfers archival file logging to a bounded background
// task. Custom loggers keep their synchronous contract. No background task may
// access Gin's recycled context or the live response writer/request body.
func (w *ResponseWriterWrapper) finalizeAfterResponse(c *gin.Context) error {
	logger, ok := w.logger.(*logging.FileRequestLogger)
	if !ok || w.requestInfo == nil || !w.preservingLogs() {
		return w.Finalize(c)
	}
	info := *w.requestInfo
	if capture := info.deferredBodyCapture; capture != nil {
		_ = capture.Finish()
		captureCopy := *capture
		info.deferredBodyCapture = &captureCopy
	}
	status := w.statusCode
	if status == 0 {
		status = w.ResponseWriter.Status()
	}
	completed := &ResponseWriterWrapper{
		body: w.body, isStreaming: w.isStreaming, chunkChannel: w.chunkChannel,
		streamWriter: w.streamWriter, streamDone: w.streamDone,
		logger: logger.Snapshot(), requestInfo: &info, statusCode: status,
		headers: w.cloneHeaders(), firstChunkTimestamp: w.firstChunkTimestamp,
		preserveLogs: w.preservingLogs(), logOnErrorOnly: w.logOnErrorOnly,
	}
	// The wrapper remains installed for outer middleware. Detach transferred
	// capture state so a later response write cannot race with finalization.
	w.body = &bytes.Buffer{}
	w.chunkChannel, w.streamWriter, w.streamDone = nil, nil, nil
	var request *http.Request
	if c.Request != nil {
		ctx := c.Request.Context()
		if ctx.Err() == nil {
			ctx = context.WithoutCancel(ctx)
		}
		request = c.Request.WithContext(ctx)
		request.Body = nil
	}
	snapshot := &gin.Context{Request: request, Keys: make(map[string]any)}
	for _, key := range []string{
		"API_REQUEST", "API_RESPONSE", "API_RESPONSE_ERROR", "API_RESPONSE_TIMESTAMP", "API_WEBSOCKET_TIMELINE",
		requestBodyOverrideContextKey, responseBodyOverrideContextKey, websocketTimelineOverrideContextKey,
		logging.APIRequestSourceContextKey, logging.APIResponseSourceContextKey,
		logging.WebsocketTimelineSourceContextKey, logging.APIWebsocketTimelineSourceContextKey,
		logging.DeferredAPIRequestContextKey,
	} {
		if value, exists := c.Get(key); exists {
			if payload, isBytes := value.([]byte); isBytes {
				value = bytes.Clone(payload)
			}
			snapshot.Keys[key] = value
		}
	}
	if logging.TryFinalizeRequestLog(func() {
		if errFinalize := completed.Finalize(snapshot); errFinalize != nil {
			log.Warn("failed to finalize request log; captured files retained when archival is enabled")
		}
	}) {
		return nil
	}
	return completed.Finalize(snapshot)
}
