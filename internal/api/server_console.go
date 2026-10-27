package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/console"
	log "github.com/sirupsen/logrus"
)

// consoleHandler is a private router of existing management operations. No HTTP
// listener, bearer secret, proxy hop or remote-management override is needed.
// Only console.Handler can dispatch here, after session, CSRF and scope checks.
func (s *Server) consoleHandler() http.Handler {
	router := gin.New()
	router.Use(gin.CustomRecovery(func(c *gin.Context, _ any) { c.AbortWithStatus(http.StatusInternalServerError) }))
	h := s.mgmt
	router.GET("/config", h.GetConsoleConfig)
	router.GET("/throughput", h.GetThroughput)
	router.GET("/events", h.Events)
	router.GET("/auth-files", h.ListAuthFiles)
	router.POST("/auth-files", h.UploadAuthFile)
	router.DELETE("/auth-files", h.DeleteAuthFile)
	router.PATCH("/auth-files/status", h.PatchAuthFileStatus)
	router.GET("/api-keys", h.GetAPIKeys)
	router.PUT("/api-keys", h.PutAPIKeys)
	router.PATCH("/api-keys", h.PatchAPIKeys)
	router.DELETE("/api-keys", h.DeleteAPIKeys)
	router.POST("/api-keys/mutate", h.MutateAPIKey)
	router.GET("/logs", h.GetLogs)
	router.GET("/request-logs", h.ListRequestLogs)
	router.GET("/request-logs/:name", h.GetRequestLogPreview)
	router.GET("/request-logs/:name/download", h.DownloadRequestLog)
	router.GET("/log-archives", h.GetLogArchives)
	router.GET("/log-archives/download", h.DownloadLogArchive)
	router.GET("/notifications", h.GetNotifications)
	router.PUT("/notifications", h.PutNotifications)
	router.POST("/notifications/test", h.TestNotifications)
	router.GET("/routing/strategy", h.GetRoutingStrategy)
	router.PUT("/routing/strategy", h.PutRoutingStrategy)
	router.PUT("/debug", h.PutDebug)
	router.PUT("/logging-to-file", h.PutLoggingToFile)
	router.PUT("/usage-statistics-enabled", h.PutUsageStatisticsEnabled)
	router.PUT("/request-log", h.PutRequestLog)
	router.PUT("/ws-auth", h.PutWebsocketAuth)
	router.PUT("/force-model-prefix", h.PutForceModelPrefix)
	router.PUT("/quota-exceeded/switch-project", h.PutSwitchProject)
	router.PUT("/quota-exceeded/switch-preview-model", h.PutSwitchPreviewModel)
	router.POST("/codex/device-auth", h.StartCodexDeviceAuth)
	router.GET("/codex/device-auth", h.GetCodexDeviceAuth)
	router.DELETE("/codex/device-auth", h.CancelCodexDeviceAuth)
	return router
}

func (s *Server) setupConsole(next http.Handler) http.Handler {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CONSOLE_ENABLED")), "true") {
		return next
	}
	cfg, err := console.Load(os.Getenv)
	if err != nil {
		// Keep the API server available, but fail console requests closed. No secret
		// values are included in configuration validation errors.
		log.Error(err)
		cfg = console.Config{}
	}
	s.console = console.New(cfg, s.consoleHandler(), func() bool { return s.cfg != nil && !s.cfg.Home.Enabled })
	return s.console.Wrap(next)
}
