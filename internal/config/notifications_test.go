package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotificationsDefaultsAndSecretSerialization(t *testing.T) {
	payload := []byte("port: 8317\nnotifications:\n  token: secret-notification-token\n")
	parsed, errParse := ParseConfigBytes(payload)
	if errParse != nil {
		t.Fatal(errParse)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if errWrite := os.WriteFile(path, payload, 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	loaded, errLoad := LoadConfig(path)
	if errLoad != nil {
		t.Fatal(errLoad)
	}
	for _, cfg := range []*Config{parsed, loaded, loaded.CloneForRuntime()} {
		if cfg.Notifications.Enabled || cfg.Notifications.WarningPercent != 20 || cfg.Notifications.CriticalPercent != 5 || cfg.Notifications.Token != "secret-notification-token" || !cfg.Notifications.ResetNotifications || !cfg.Notifications.BankedResetNotifications {
			t.Fatalf("notification defaults or clone are incorrect")
		}
		encoded, errJSON := json.Marshal(cfg)
		if errJSON != nil || strings.Contains(string(encoded), "secret-notification-token") || strings.Contains(string(encoded), `"notifications"`) {
			t.Fatal("general config JSON must omit notifications")
		}
	}
	if errSave := SaveConfigPreserveComments(path, loaded); errSave != nil {
		t.Fatal(errSave)
	}
	again, errLoad := LoadConfig(path)
	if errLoad != nil || again.Notifications.Token != "secret-notification-token" {
		t.Fatal("saved YAML must preserve the delivery token")
	}
}

func TestNotificationResetPreferencesRoundTrip(t *testing.T) {
	for _, reset := range []bool{false, true} {
		for _, banked := range []bool{false, true} {
			t.Run(fmt.Sprintf("reset=%t/banked=%t", reset, banked), func(t *testing.T) {
				payload := []byte(fmt.Sprintf("port: 8317\nnotifications:\n  reset-notifications: %t\n  banked-reset-notifications: %t\n", reset, banked))
				parsed, errParse := ParseConfigBytes(payload)
				if errParse != nil {
					t.Fatal(errParse)
				}
				path := filepath.Join(t.TempDir(), "config.yaml")
				if errWrite := os.WriteFile(path, payload, 0600); errWrite != nil {
					t.Fatal(errWrite)
				}
				if errSave := SaveConfigPreserveComments(path, parsed); errSave != nil {
					t.Fatal(errSave)
				}
				loaded, errLoad := LoadConfig(path)
				if errLoad != nil {
					t.Fatal(errLoad)
				}
				for _, cfg := range []*Config{parsed, loaded, loaded.CloneForRuntime()} {
					if cfg.Notifications.Enabled || cfg.Notifications.ResetNotifications != reset || cfg.Notifications.BankedResetNotifications != banked {
						t.Fatal("reset preferences or master opt-in changed during load/save/clone")
					}
				}
			})
		}
	}
}

func TestNotificationsValidation(t *testing.T) {
	valid := DefaultNotificationsConfig()
	valid.Enabled, valid.URL, valid.Topic = true, "http://127.0.0.1:8080/ntfy", "quota-alerts"
	if errValidate := valid.Validate(); errValidate != nil {
		t.Fatal(errValidate)
	}
	for name, change := range map[string]func(*NotificationsConfig){
		"missing URL":            func(c *NotificationsConfig) { c.URL = "" },
		"missing topic":          func(c *NotificationsConfig) { c.Topic = "" },
		"URL credentials":        func(c *NotificationsConfig) { c.URL = "https://user:secret@example.com" },
		"URL query":              func(c *NotificationsConfig) { c.URL = "https://example.com?token=secret" },
		"URL fragment":           func(c *NotificationsConfig) { c.URL = "https://example.com#topic" },
		"URL scheme":             func(c *NotificationsConfig) { c.URL = "file:///tmp/file" },
		"topic path":             func(c *NotificationsConfig) { c.Topic = "../other" },
		"token header injection": func(c *NotificationsConfig) { c.Token = "token\r\nExtra: value" },
		"equal thresholds":       func(c *NotificationsConfig) { c.CriticalPercent = c.WarningPercent },
		"negative threshold":     func(c *NotificationsConfig) { c.CriticalPercent = -1 },
		"large threshold":        func(c *NotificationsConfig) { c.WarningPercent = 101 },
		"NaN threshold":          func(c *NotificationsConfig) { c.WarningPercent = math.NaN() },
		"infinite threshold":     func(c *NotificationsConfig) { c.CriticalPercent = math.Inf(1) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := valid
			change(&invalid)
			if invalid.Validate() == nil {
				t.Fatal("expected invalid settings to be rejected")
			}
		})
	}
	if errValidate := DefaultNotificationsConfig().Validate(); errValidate != nil {
		t.Fatal("disabled notifications must allow no destination")
	}
}
