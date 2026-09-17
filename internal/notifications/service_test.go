package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func testSettings(url string) config.NotificationsConfig {
	return config.NotificationsConfig{Enabled: true, URL: url, Topic: "quota-alerts", Token: "private-token", WarningPercent: 20, CriticalPercent: 5}
}

func TestDeliveryEpisodesEscalationRecoveryAndWindowReset(t *testing.T) {
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/quota-alerts" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("incorrect ntfy request")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "remaining") || strings.Contains(string(body), "private-token") {
			t.Errorf("unexpected body: %s", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	now := time.Unix(1787279282, 0)
	account := observedAccount("codex", "80", now)
	service := New(func() []*auth.Auth { return []*auth.Auth{account} })
	service.now = func() time.Time { return now }
	service.Configure(testSettings(server.URL))
	poll := func(used string, want int32) {
		t.Helper()
		account.Quota.ObservedAt = now
		account.Quota.Signals["X-Codex-Primary-Used-Percent"] = used
		service.poll(context.Background())
		if sends.Load() != want {
			t.Fatalf("used=%s sends=%d, want %d", used, sends.Load(), want)
		}
	}
	poll("80", 1)
	poll("84", 1)
	poll("95", 2)
	poll("99", 2)
	poll("78", 2) // 22% does not cross the recovery hysteresis.
	poll("96", 2)
	poll("74", 2) // 26% silently re-arms the warning and critical episode.
	poll("81", 3)
	account.Quota.Signals["X-Codex-Primary-Reset-At"] = fmt.Sprint(now.Add(time.Minute).Unix())
	poll("90", 3)
	now = now.Add(2 * time.Minute)
	account.Quota.Signals["X-Codex-Primary-Reset-At"] = fmt.Sprint(now.Add(time.Hour).Unix())
	poll("90", 4)
	if status := service.Status(); status.LastSuccessAt == nil || status.LastError != "" || status.InFlight {
		t.Fatalf("status = %+v", status)
	}
}

func TestFailuresBackOffWithoutConsumingEpisodeOrExposingSecrets(t *testing.T) {
	var sends atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("private-token secret upstream response"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	now := time.Unix(1787279282, 0)
	account := observedAccount("codex", "90", now)
	service := New(func() []*auth.Auth { return []*auth.Auth{account} })
	service.now = func() time.Time { return now }
	service.Configure(testSettings(server.URL))
	service.poll(context.Background())
	service.poll(context.Background())
	if sends.Load() != 1 {
		t.Fatal("failed delivery retried without backoff")
	}
	status := service.Status()
	if status.NextRetryAt == nil || !status.NextRetryAt.Equal(now.Add(pollInterval)) {
		t.Fatalf("status = %+v", status)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "private-token") || strings.Contains(string(encoded), server.URL) {
		t.Fatalf("status exposed credentials: %s", encoded)
	}
	now = *status.NextRetryAt
	service.poll(context.Background())
	if sends.Load() != 2 || !service.Status().NextRetryAt.Equal(now.Add(2*pollInterval)) {
		t.Fatal("failure backoff did not increase")
	}
	now = *service.Status().NextRetryAt
	fail.Store(false)
	service.poll(context.Background())
	service.poll(context.Background())
	if sends.Load() != 3 || service.Status().NextRetryAt != nil {
		t.Fatal("successful retry did not acknowledge episode")
	}
}

func TestNotificationsDisabledUntilEnabledAndExplicitTest(t *testing.T) {
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1) }))
	defer server.Close()
	now := time.Now()
	service := New(func() []*auth.Auth { return []*auth.Auth{observedAccount("codex", "99", now)} })
	service.poll(context.Background())
	cfg := testSettings(server.URL)
	cfg.Enabled = false
	service.Configure(cfg)
	service.poll(context.Background())
	if sends.Load() != 0 {
		t.Fatal("disabled configuration sent an automatic alert")
	}
	if errSend := service.SendTest(context.Background()); errSend != nil {
		t.Fatal(errSend)
	}
	if sends.Load() != 1 {
		t.Fatal("explicit test was not sent")
	}
	cfg.Enabled = true
	service.Configure(cfg)
	service.poll(context.Background())
	if sends.Load() != 2 {
		t.Fatal("enabling alerts did not send low-quota observation")
	}
}

func TestDeliveryDoesNotFollowRedirectOrReturnResponseBody(t *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte("private-token"))
	}))
	defer server.Close()
	service := New(nil)
	service.Configure(testSettings(server.URL))
	errSend := service.SendTest(context.Background())
	if errSend == nil || redirected.Load() || strings.Contains(errSend.Error(), "private-token") {
		t.Fatalf("redirect=%v error=%v", redirected.Load(), errSend)
	}
}

func TestConfigureCancelsObsoleteDelivery(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	service := New(nil)
	cfg := testSettings(server.URL)
	service.Configure(cfg)
	done := make(chan error, 1)
	go func() { done <- service.SendTest(context.Background()) }()
	<-entered
	if !service.Status().InFlight {
		t.Fatal("in-flight delivery not visible")
	}
	if errSend := service.SendTest(context.Background()); errSend != ErrBusy {
		t.Fatalf("concurrent test error = %v", errSend)
	}
	cfg.Enabled = false
	service.Configure(cfg)
	select {
	case errSend := <-done:
		if errSend == nil {
			t.Fatal("obsolete delivery did not report cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("configuration change failed to cancel delivery")
	}
	status := service.Status()
	if status.Enabled || status.InFlight || status.LastError != "" || status.LastSuccessAt != nil {
		t.Fatalf("obsolete delivery changed new settings status: %+v", status)
	}
}

func TestRunSendsImmediatelyAndStopsOnCancellation(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	now := time.Now()
	service := New(func() []*auth.Auth { return []*auth.Auth{observedAccount("codex", "99", now)} })
	service.Configure(testSettings(server.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { service.Run(ctx); close(done) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("notification worker failed to stop")
	}
}

func TestInvalidSettingsFailClosedAndStatusIsSnapshot(t *testing.T) {
	service := New(nil)
	cfg := testSettings("https://user:secret@example.invalid")
	service.Configure(cfg)
	if status := service.Status(); status.Configured || status.LastError == "" {
		t.Fatalf("invalid configuration status = %+v", status)
	}
	if errSend := service.SendTest(context.Background()); errSend != ErrNotConfigured {
		t.Fatalf("invalid configuration send error = %v", errSend)
	}
	stamp := time.Unix(123, 0)
	service.status.LastSuccessAt = &stamp
	status := service.Status()
	*status.LastSuccessAt = time.Time{}
	if service.Status().LastSuccessAt.IsZero() {
		t.Fatal("status caller mutated service state")
	}
}

func TestRunCanRestartAfterShutdown(t *testing.T) {
	var cancel context.CancelFunc
	calls := 0
	service := New(func() []*auth.Auth {
		calls++
		cancel()
		return nil
	})
	service.Configure(testSettings("https://unused.invalid"))
	for range 2 {
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		service.Run(ctx)
		cancel()
	}
	if calls != 2 {
		t.Fatalf("source calls across restarts = %d, want 2", calls)
	}
}
