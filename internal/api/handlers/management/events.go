package management

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementevents"
)

// Events sends invalidations; existing authenticated reads remain the source of truth.
// Reconnecting clients refresh their snapshots, so no event history is required.
func (h *Handler) Events(c *gin.Context) {
	logging.SkipGinRequestLogging(c)
	stopped := make(chan struct{})
	h.eventsMu.Lock()
	if h.eventsClosed {
		h.eventsMu.Unlock()
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if h.eventStreams == nil {
		h.eventStreams = make(map[chan struct{}]struct{})
	}
	h.eventStreams[stopped] = struct{}{}
	h.eventsMu.Unlock()
	defer func() { h.eventsMu.Lock(); delete(h.eventStreams, stopped); h.eventsMu.Unlock() }()
	changes, unsubscribe := managementevents.Subscribe()
	defer unsubscribe()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-store")
	c.Header("X-Accel-Buffering", "no")
	if _, err := fmt.Fprint(c.Writer, ": connected\n\n"); err != nil {
		return
	}
	c.Writer.Flush()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-stopped:
			return
		case <-c.Request.Context().Done():
			return
		case topics := <-changes:
			// Batch bursts of request completions without periodically querying state.
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-stopped:
				timer.Stop()
				return
			case <-c.Request.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			select {
			case more := <-changes:
				topics |= more
			default:
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %d\n\n", topics); err != nil {
				return
			}
			c.Writer.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": heartbeat\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// CloseEventStreams lets graceful HTTP shutdown finish without waiting for browsers.
func (h *Handler) CloseEventStreams() {
	h.eventsMu.Lock()
	defer h.eventsMu.Unlock()
	if h.eventsClosed {
		return
	}
	h.eventsClosed = true
	for stream := range h.eventStreams {
		close(stream)
	}
}
