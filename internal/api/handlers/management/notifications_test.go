package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/notifications"
)

type notificationServiceStub struct {
	calls      int
	err        error
	configured *config.NotificationsConfig
}

func (s *notificationServiceStub) Configure(settings config.NotificationsConfig) {
	s.configured = &settings
}

func (s *notificationServiceStub) Status() notifications.Status {
	return notifications.Status{Enabled: true, Configured: true}
}

func (s *notificationServiceStub) SendTest(context.Context) error {
	s.calls++
	return s.err
}

func newNotificationHandler(t *testing.T) *Handler {
	t.Helper()
	settings := config.DefaultNotificationsConfig()
	settings.Enabled, settings.URL, settings.Topic, settings.Token = true, "https://ntfy.example.com", "quota", "stored-token"
	return &Handler{cfg: &config.Config{Notifications: settings}, configFilePath: writeTestConfigFile(t)}
}

func notificationRequest(t *testing.T, h *Handler, method, body string, test bool) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/v0/management/notifications", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	switch {
	case test:
		h.TestNotifications(c)
	case method == http.MethodPut:
		h.PutNotifications(c)
	default:
		h.GetNotifications(c)
	}
	return w
}

func TestNotificationSettingsRedactAndPreserveToken(t *testing.T) {
	h := newNotificationHandler(t)
	h.SetNotifications(&notificationServiceStub{})
	previous := h.cfg
	reloaded := false
	h.SetConfigReloadHook(func(_ context.Context, cfg *config.Config) {
		// The handler mutex must be released when the reload hook runs.
		h.SetConfig(cfg)
		reloaded = true
	})
	response := notificationRequest(t, h, http.MethodPut, `{"warning_percent":25}`, false)
	if response.Code != 200 || !reloaded {
		t.Fatalf("save/reload failed: %d %s", response.Code, response.Body.String())
	}
	if previous.Notifications.WarningPercent != 20 || h.cfg.Notifications.WarningPercent != 25 {
		t.Fatal("saving must publish a new config snapshot")
	}
	if strings.Contains(response.Body.String(), "stored-token") || strings.Contains(response.Body.String(), `"token":`) {
		t.Fatal("response leaked token")
	}
	var decoded notificationSettingsResponse
	if errJSON := json.Unmarshal(response.Body.Bytes(), &decoded); errJSON != nil || !decoded.TokenConfigured || !decoded.Status.Configured {
		t.Fatal("missing safe token/delivery status")
	}
	loaded, errLoad := config.LoadConfig(h.configFilePath)
	if errLoad != nil || loaded.Notifications.Token != "stored-token" || loaded.Notifications.WarningPercent != 25 {
		t.Fatal("omitted token must be preserved on disk")
	}
	response = notificationRequest(t, h, http.MethodPut, `{"clear_token":true}`, false)
	if response.Code != 200 || h.cfg.Notifications.Token != "" || !strings.Contains(response.Body.String(), `"token_configured":false`) {
		t.Fatal("explicit token clear failed")
	}
	loaded, errLoad = config.LoadConfig(h.configFilePath)
	if errLoad != nil || loaded.Notifications.Token != "" {
		t.Fatal("explicit token clear must persist on disk")
	}
	response = notificationRequest(t, h, http.MethodPut, `{"token":"replacement-token"}`, false)
	if response.Code != 200 || h.cfg.Notifications.Token != "replacement-token" || strings.Contains(response.Body.String(), "replacement-token") {
		t.Fatal("token replacement failed or leaked")
	}
}

func TestNotificationSettingsRejectInvalidWithoutSaving(t *testing.T) {
	for _, body := range []string{
		`{"url":"https://user:secret@example.com"}`,
		`{"topic":"../other"}`,
		`{"warning_percent":4}`,
		`{"token":""}`,
		`{"token":"new-token","clear_token":true}`,
		`{"token":"secret\r\nheader:value"}`,
	} {
		t.Run(body, func(t *testing.T) {
			h := newNotificationHandler(t)
			previous := h.cfg
			response := notificationRequest(t, h, http.MethodPut, body, false)
			if response.Code != 400 || h.cfg != previous || h.cfg.Notifications.Token != "stored-token" {
				t.Fatalf("invalid request changed settings: %d", response.Code)
			}
		})
	}
}

func TestNotificationSaveFailureRestoresConfig(t *testing.T) {
	h := newNotificationHandler(t)
	h.configFilePath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	previous := h.cfg
	response := notificationRequest(t, h, http.MethodPut, `{"warning_percent":25}`, false)
	if response.Code != 500 || h.cfg != previous || h.cfg.Notifications.WarningPercent != 20 {
		t.Fatal("persistence failure must restore the previous config")
	}
}

func TestNotificationTestUsesServiceAndSanitizesFailure(t *testing.T) {
	h := newNotificationHandler(t)
	service := &notificationServiceStub{}
	h.SetNotifications(service)
	response := notificationRequest(t, h, http.MethodPost, `{"url":"https://untrusted-override.example"}`, true)
	if response.Code != 200 || service.calls != 1 || h.cfg.Notifications.URL != "https://ntfy.example.com" {
		t.Fatal("test must use service configured from saved settings")
	}
	service.err = errors.New("network error containing stored-token")
	response = notificationRequest(t, h, http.MethodPost, `{}`, true)
	if response.Code != 502 || strings.Contains(response.Body.String(), "stored-token") {
		t.Fatal("test failure must not expose upstream details")
	}
	h.cfg.Notifications.Enabled = false
	service.err = nil
	response = notificationRequest(t, h, http.MethodPost, `{}`, true)
	if response.Code != 200 || service.calls != 3 {
		t.Fatal("saved destinations can be tested while automatic alerts are disabled")
	}
	h.cfg.Notifications.Topic = ""
	response = notificationRequest(t, h, http.MethodPost, `{}`, true)
	if response.Code != 400 || service.calls != 3 {
		t.Fatal("unconfigured destination must not send")
	}
}

func TestNotificationSaveConfiguresWithoutReloadHook(t *testing.T) {
	h := newNotificationHandler(t)
	service := &notificationServiceStub{}
	h.SetNotifications(service)
	response := notificationRequest(t, h, http.MethodPut, `{"warning_percent":25}`, false)
	if response.Code != 200 || service.configured == nil || service.configured.WarningPercent != 25 || service.configured.Token != "stored-token" {
		t.Fatal("save must reconfigure runtime delivery without a reload hook")
	}
}

func TestNotificationResetPreferencesPartialUpdates(t *testing.T) {
	h := newNotificationHandler(t)
	service := &notificationServiceStub{}
	h.SetNotifications(service)
	for _, step := range []struct {
		body   string
		reset  bool
		banked bool
	}{
		{`{}`, true, true},
		{`{"reset_notifications":false}`, false, true},
		{`{"warning_percent":25}`, false, true},
		{`{"banked_reset_notifications":false}`, false, false},
		{`{"reset_notifications":true}`, true, false},
		{`{"banked_reset_notifications":true}`, true, true},
	} {
		response := notificationRequest(t, h, http.MethodPut, step.body, false)
		if response.Code != http.StatusOK {
			t.Fatalf("save failed: %d %s", response.Code, response.Body.String())
		}
		var fields map[string]any
		if errJSON := json.Unmarshal(response.Body.Bytes(), &fields); errJSON != nil {
			t.Fatal(errJSON)
		}
		if fields["reset_notifications"] != step.reset || fields["banked_reset_notifications"] != step.banked {
			t.Fatalf("response must include exact preferences: %s", response.Body.String())
		}
		if strings.Contains(response.Body.String(), "stored-token") {
			t.Fatal("token leaked")
		}
		loaded, errLoad := config.LoadConfig(h.configFilePath)
		if errLoad != nil {
			t.Fatal(errLoad)
		}
		if loaded.Notifications.ResetNotifications != step.reset || loaded.Notifications.BankedResetNotifications != step.banked || loaded.Notifications.Token != "stored-token" {
			t.Fatal("saving preferences must preserve explicit false and the existing token")
		}
		if service.configured == nil || service.configured.ResetNotifications != step.reset || service.configured.BankedResetNotifications != step.banked {
			t.Fatal("reset preferences must reach the runtime service")
		}
	}
}

func TestNotificationZeroConfigResetDefaults(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	response := notificationRequest(t, h, http.MethodGet, "", false)
	var settings notificationSettingsResponse
	if errJSON := json.Unmarshal(response.Body.Bytes(), &settings); errJSON != nil {
		t.Fatal(errJSON)
	}
	if response.Code != http.StatusOK || settings.Enabled || !settings.ResetNotifications || !settings.BankedResetNotifications {
		t.Fatal("zero configuration must report reset defaults without enabling automatic notifications")
	}
}
