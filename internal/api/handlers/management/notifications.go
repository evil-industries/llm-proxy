package management

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/notifications"
)

// NotificationService exposes delivery state without exposing stored secrets.
type NotificationService interface {
	Configure(config.NotificationsConfig)
	Status() notifications.Status
	SendTest(context.Context) error
}

func (h *Handler) SetNotifications(service NotificationService) {
	h.mu.Lock()
	h.notifications = service
	h.mu.Unlock()
}

type notificationSettingsResponse struct {
	config.NotificationsConfig
	TokenConfigured bool                 `json:"token_configured"`
	Status          notifications.Status `json:"status"`
}

func (h *Handler) GetNotifications(c *gin.Context) {
	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration is unavailable"})
		return
	}
	settings := notificationSettingsWithDefaults(h.cfg.Notifications)
	service := h.notifications
	h.mu.Unlock()
	response := notificationSettingsResponse{NotificationsConfig: settings, TokenConfigured: settings.Token != ""}
	// Clear the copy as well as excluding Token from JSON serialization.
	response.Token = ""
	if service != nil {
		response.Status = service.Status()
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) PutNotifications(c *gin.Context) {
	var body struct {
		Enabled                  *bool    `json:"enabled"`
		URL                      *string  `json:"url"`
		Topic                    *string  `json:"topic"`
		Token                    *string  `json:"token"`
		ClearToken               bool     `json:"clear_token"`
		WarningPercent           *float64 `json:"warning_percent"`
		CriticalPercent          *float64 `json:"critical_percent"`
		ResetNotifications       *bool    `json:"reset_notifications"`
		BankedResetNotifications *bool    `json:"banked_reset_notifications"`
	}
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid notification settings"})
		return
	}
	if body.Token != nil && body.ClearToken {
		c.JSON(http.StatusBadRequest, gin.H{"error": "set or clear the token, not both"})
		return
	}
	if body.Token != nil && strings.TrimSpace(*body.Token) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "use clear_token to remove the saved token"})
		return
	}
	// Serialize saves with reloads, while releasing h.mu before invoking hooks.
	h.reloadMu.Lock()
	defer h.reloadMu.Unlock()
	h.mu.Lock()
	locked := true
	defer func() {
		if locked {
			h.mu.Unlock()
		}
	}()
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "configuration is unavailable"})
		return
	}
	next := notificationSettingsWithDefaults(h.cfg.Notifications)
	if body.Enabled != nil {
		next.Enabled = *body.Enabled
	}
	if body.URL != nil {
		next.URL = strings.TrimSpace(*body.URL)
	}
	if body.Topic != nil {
		next.Topic = strings.TrimSpace(*body.Topic)
	}
	if body.Token != nil {
		next.Token = *body.Token
	}
	if body.ClearToken {
		next.Token = ""
	}
	if body.WarningPercent != nil {
		next.WarningPercent = *body.WarningPercent
	}
	if body.CriticalPercent != nil {
		next.CriticalPercent = *body.CriticalPercent
	}
	if body.ResetNotifications != nil {
		next.ResetNotifications = *body.ResetNotifications
	}
	if body.BankedResetNotifications != nil {
		next.BankedResetNotifications = *body.BankedResetNotifications
	}
	if errValidate := next.Validate(); errValidate != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errValidate.Error()})
		return
	}
	previous := h.cfg
	h.cfg = previous.CloneForRuntime()
	h.cfg.Notifications = next
	snapshot, saved := h.saveConfigAndSnapshotLocked(c)
	if !saved {
		h.cfg = previous
		return
	}
	service := h.notifications
	configureDirectly := h.configReloadHook == nil
	h.mu.Unlock()
	locked = false
	h.reloadConfigAfterManagementSaveLocked(c.Request.Context(), snapshot)
	if configureDirectly && service != nil {
		service.Configure(next)
	}
	h.GetNotifications(c)
}

// TestNotifications deliberately accepts no destination overrides: only saved settings are used.
func (h *Handler) TestNotifications(c *gin.Context) {
	h.mu.Lock()
	service := h.notifications
	configured := false
	if h.cfg != nil {
		settings := notificationSettingsWithDefaults(h.cfg.Notifications)
		// A test verifies the saved destination without enabling automatic alerts.
		settings.Enabled = true
		configured = settings.Validate() == nil
	}
	h.mu.Unlock()
	if !configured {
		c.JSON(http.StatusBadRequest, gin.H{"error": "save a valid notification URL and topic before sending a test"})
		return
	}
	if service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "notification delivery is unavailable"})
		return
	}
	if errSend := service.SendTest(c.Request.Context()); errSend != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "notification delivery failed; check the saved settings and delivery status"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func notificationSettingsWithDefaults(settings config.NotificationsConfig) config.NotificationsConfig {
	if settings == (config.NotificationsConfig{}) {
		return config.DefaultNotificationsConfig()
	}
	if settings.WarningPercent == 0 && settings.CriticalPercent == 0 {
		defaults := config.DefaultNotificationsConfig()
		settings.WarningPercent = defaults.WarningPercent
		settings.CriticalPercent = defaults.CriticalPercent
	}
	return settings
}
