package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestNotificationWorkerUsesRuntimeQuotaWithoutBrowser(t *testing.T) {
	type publication struct{ path, token, priority, body string }
	received := make(chan publication, 2)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- publication{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Priority"), string(body)}
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	server := newTestServer(t)
	_, errRegister := server.handlers.AuthManager.Register(context.Background(), &auth.Auth{
		ID: "credential-secret", Provider: "codex", Label: "Agent account",
		Quota: auth.QuotaState{ObservedAt: time.Now(), Signals: map[string]string{"X-Codex-Primary-Used-Percent": "92"}},
	})
	if errRegister != nil {
		t.Fatal(errRegister)
	}
	cfg := server.cfg.CloneForRuntime()
	cfg.Notifications = config.NotificationsConfig{
		Enabled: true, URL: destination.URL + "/ntfy", Topic: "quota", Token: "test-token",
		WarningPercent: 20, CriticalPercent: 5,
	}
	server.UpdateClients(cfg)
	server.startNotifications()
	t.Cleanup(func() {
		if errStop := server.stopNotifications(context.Background()); errStop != nil {
			t.Error(errStop)
		}
	})
	select {
	case got := <-received:
		if got.path != "/ntfy/quota" || got.token != "Bearer test-token" || got.priority != "high" {
			t.Fatalf("unexpected publication: %+v", got)
		}
		if !strings.Contains(got.body, "8.0% remaining") || !strings.Contains(got.body, "Agent account") || strings.Contains(got.body, "credential-secret") {
			t.Fatalf("unexpected notification body: %q", got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not publish the observed low quota")
	}
	if errStop := server.stopNotifications(context.Background()); errStop != nil {
		t.Fatal(errStop)
	}
	cfg = cfg.CloneForRuntime()
	cfg.Notifications.Enabled = false
	server.UpdateClients(cfg)
	if server.notifications.Status().Enabled {
		t.Fatal("runtime reload did not disable notifications")
	}
}

func TestNotificationWorkerShutdownCancelsPendingDelivery(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer destination.Close()
	server := newTestServer(t)
	_, errRegister := server.handlers.AuthManager.Register(context.Background(), &auth.Auth{
		ID: "account", Provider: "claude",
		Quota: auth.QuotaState{ObservedAt: time.Now(), Signals: map[string]string{"Anthropic-Ratelimit-Unified-5h-Utilization": "0.99"}},
	})
	if errRegister != nil {
		t.Fatal(errRegister)
	}
	server.notifications.Configure(config.NotificationsConfig{
		Enabled: true, URL: destination.URL, Topic: "quota", WarningPercent: 20, CriticalPercent: 5,
	})
	server.startNotifications()
	t.Cleanup(func() { _ = server.stopNotifications(context.Background()) })
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if errStop := server.stopNotifications(ctx); errStop != nil {
		t.Fatal(errStop)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("shutdown did not cancel pending HTTP delivery")
	}
}
