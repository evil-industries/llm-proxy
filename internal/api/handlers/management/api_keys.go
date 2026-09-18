package management

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// MutateAPIKey changes one identity without replacing a previously read key list.
// Unlike the legacy list API, removal cannot disable authentication by removing
// the final effective key. Key material stays in the body instead of the URL.
func (h *Handler) MutateAPIKey(c *gin.Context) {
	var body struct {
		Action string `json:"action"`
		Value  string `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	value := strings.TrimSpace(body.Value)
	if value == "" || (body.Action != "add" && body.Action != "remove") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action and non-empty value are required"})
		return
	}
	// Keep reload hooks from installing an earlier config between the read and save.
	h.reloadMu.Lock()
	defer h.reloadMu.Unlock()
	h.mu.Lock()
	locked := true
	defer func() {
		if locked {
			h.mu.Unlock()
		}
	}()
	current := h.cfg.APIKeys
	next := make([]string, 0, len(current)+1)
	found := false
	effectiveRemaining := false
	for _, key := range current {
		if strings.TrimSpace(key) == value {
			found = true
			if body.Action == "remove" {
				continue
			}
		}
		next = append(next, key)
		effectiveRemaining = effectiveRemaining || strings.TrimSpace(key) != ""
	}
	if body.Action == "add" {
		if found {
			c.JSON(http.StatusConflict, gin.H{"error": "API key already exists"})
			return
		}
		next = append(next, value)
	} else {
		if !found {
			c.JSON(http.StatusConflict, gin.H{"error": "API key no longer exists"})
			return
		}
		if !effectiveRemaining {
			c.JSON(http.StatusConflict, gin.H{"error": "cannot remove the final API key"})
			return
		}
	}
	// Watcher and runtime consumers may still hold the current config. Keep
	// their snapshot immutable while the saved update waits to be reloaded.
	previousConfig := h.cfg
	h.cfg = previousConfig.CloneForRuntime()
	h.cfg.APIKeys = next
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		h.cfg = previousConfig
		return
	}
	h.mu.Unlock()
	locked = false
	h.reloadConfigAfterManagementSaveLocked(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
