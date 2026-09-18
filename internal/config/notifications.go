package config

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
)

// NotificationsConfig configures opt-in ntfy quota alerts from the gateway.
type NotificationsConfig struct {
	Enabled                  bool    `yaml:"enabled" json:"enabled"`
	URL                      string  `yaml:"url" json:"url"`
	Topic                    string  `yaml:"topic" json:"topic"`
	Token                    string  `yaml:"token" json:"-"`
	WarningPercent           float64 `yaml:"warning-percent" json:"warning_percent"`
	CriticalPercent          float64 `yaml:"critical-percent" json:"critical_percent"`
	ResetNotifications       bool    `yaml:"reset-notifications" json:"reset_notifications"`
	BankedResetNotifications bool    `yaml:"banked-reset-notifications" json:"banked_reset_notifications"`
}

func DefaultNotificationsConfig() NotificationsConfig {
	return NotificationsConfig{WarningPercent: 20, CriticalPercent: 5, ResetNotifications: true, BankedResetNotifications: true}
}

var notificationTopicPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Validate checks the destination and thresholds without making network requests.
func (c NotificationsConfig) Validate() error {
	if math.IsNaN(c.WarningPercent) || math.IsInf(c.WarningPercent, 0) ||
		math.IsNaN(c.CriticalPercent) || math.IsInf(c.CriticalPercent, 0) ||
		c.CriticalPercent < 0 || c.WarningPercent > 100 || c.CriticalPercent >= c.WarningPercent {
		return fmt.Errorf("notifications thresholds must satisfy 0 <= critical-percent < warning-percent <= 100")
	}
	if c.URL != "" {
		u, errParse := url.Parse(c.URL)
		if errParse != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
			u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			return fmt.Errorf("notifications URL must be HTTP or HTTPS without credentials, query, or fragment")
		}
	}
	if c.Topic != "" && !notificationTopicPattern.MatchString(c.Topic) {
		return fmt.Errorf("notifications topic may contain only letters, numbers, underscores, and hyphens")
	}
	if strings.IndexFunc(c.Token, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return fmt.Errorf("notifications token contains invalid characters")
	}
	if c.Enabled && (c.URL == "" || c.Topic == "") {
		return fmt.Errorf("notifications URL and topic are required when enabled")
	}
	return nil
}
