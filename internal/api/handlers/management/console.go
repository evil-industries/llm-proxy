package management

import "github.com/gin-gonic/gin"

// GetConsoleConfig is the scoped console settings view. The upstream-compatible
// GetConfig endpoint remains unchanged. Provider credentials, storage settings,
// management keys, plugin config and other private configuration are omitted.
func (h *Handler) GetConsoleConfig(c *gin.Context) {
	cfg := h.configSnapshot()
	if cfg == nil {
		c.JSON(200, gin.H{})
		return
	}
	c.JSON(200, gin.H{
		"debug":                    cfg.Debug,
		"logging-to-file":          cfg.LoggingToFile,
		"usage-statistics-enabled": cfg.UsageStatisticsEnabled,
		"request-log":              cfg.RequestLog,
		"ws-auth":                  cfg.WebsocketAuth,
		"force-model-prefix":       cfg.ForceModelPrefix,
		"request-retry":            cfg.RequestRetry,
		"max-retry-credentials":    cfg.MaxRetryCredentials,
		"max-retry-interval":       cfg.MaxRetryInterval,
		"routing":                  gin.H{"strategy": cfg.Routing.Strategy},
		"quota-exceeded":           gin.H{"switch-project": cfg.QuotaExceeded.SwitchProject, "switch-preview-model": cfg.QuotaExceeded.SwitchPreviewModel},
	})
}
