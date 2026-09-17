package notifications

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestResetNotificationsRequireFreshConfirmationAndDeduplicate(t *testing.T) {
	titles := make(chan string, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { titles <- r.Header.Get("Title") }))
	defer server.Close()
	now := time.Unix(1787279282, 0)
	account := observedAccount("codex", "90", now)
	reset := now.Add(time.Minute)
	account.Quota.Signals["X-Codex-Primary-Reset-At"] = fmt.Sprint(reset.Unix())
	service := New(func() []*auth.Auth { return []*auth.Auth{account} })
	service.now = func() time.Time { return now }
	cfg := testSettings(server.URL)
	cfg.ResetNotifications = true
	service.Configure(cfg)
	poll := func(want ...string) {
		t.Helper()
		service.poll(context.Background())
		if len(titles) != len(want) {
			t.Fatalf("got %d alerts, want %v", len(titles), want)
		}
		for _, expected := range want {
			if got := <-titles; got != expected {
				t.Fatalf("title %q, want %q", got, expected)
			}
		}
	}
	poll("Low remaining quota")
	now = reset.Add(time.Second)
	poll() // An elapsed deadline without a new observation is not confirmation.
	account.Quota.ObservedAt = now
	account.Quota.Signals["X-Codex-Primary-Reset-At"] = fmt.Sprint(now.Add(time.Hour).Unix())
	account.Quota.Signals["X-Codex-Primary-Used-Percent"] = "1"
	poll("Quota window reset observed")
	poll()
	now = now.Add(time.Second)
	account.Quota.ObservedAt = now
	account.Quota.Signals["X-Codex-Primary-Used-Percent"] = "90"
	poll("Low remaining quota")
	now = now.Add(time.Second)
	account.Quota.ObservedAt = now
	account.Quota.Signals["X-Codex-Primary-Used-Percent"] = "78"
	poll() // Small fluctuations above the warning level do not re-arm recovery.
	now = now.Add(time.Second)
	account.Quota.ObservedAt = now
	account.Quota.Signals["X-Codex-Primary-Used-Percent"] = "10"
	poll("Quota recovered") // Early/manual recovery need not wait for the timer.
	poll()
}

func TestResetAlertsRetryWithoutLosingRecovery(t *testing.T) {
	var attempts atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Title") == "Quota recovered" {
			attempts.Add(1)
			if fail.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		}
	}))
	defer server.Close()
	now := time.Unix(1787279282, 0)
	account := observedAccount("claude", "0.99", now)
	service := New(func() []*auth.Auth { return []*auth.Auth{account} })
	service.now = func() time.Time { return now }
	cfg := testSettings(server.URL)
	cfg.ResetNotifications = true
	service.Configure(cfg)
	service.poll(context.Background())
	now = now.Add(time.Second)
	account.Quota.ObservedAt = now
	account.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Utilization"] = "0.01"
	fail.Store(true)
	service.poll(context.Background())
	service.poll(context.Background())
	if attempts.Load() != 1 || service.Status().NextRetryAt == nil {
		t.Fatal("missing recovery retry/backoff")
	}
	now = *service.Status().NextRetryAt
	fail.Store(false)
	service.poll(context.Background())
	service.poll(context.Background())
	if attempts.Load() != 2 || service.Status().NextRetryAt != nil {
		t.Fatal("recovery retry was lost or duplicated")
	}
}

func TestBankedAlertsUseAuthoritativeCountAndRetry(t *testing.T) {
	var attempts atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	now := time.Unix(1787279282, 0)
	account := observedAccount("codex", "20", now)
	service := New(func() []*auth.Auth { return []*auth.Auth{account} })
	service.now = func() time.Time { return now }
	cfg := testSettings(server.URL)
	cfg.BankedResetNotifications = true
	service.Configure(cfg)
	poll := func(count string, want int32) {
		t.Helper()
		now = now.Add(time.Second)
		account.Quota.ObservedAt = now
		delete(account.Quota.Signals, bankedCountHeader)
		if count != "" {
			account.Quota.Signals[bankedCountHeader] = count
		}
		service.poll(context.Background())
		if attempts.Load() != want {
			t.Fatalf("count %q: attempts %d, want %d", count, attempts.Load(), want)
		}
	}
	poll("", 0)
	poll("2", 1) // First explicit positive count is useful even without a baseline.
	poll("2", 1)
	poll("", 1) // Omission does not manufacture a zero-to-positive transition.
	poll("2", 1)
	poll("1", 1) // Redemption or expiration is silent.
	fail.Store(true)
	poll("2", 2)
	poll("2", 2)
	now = *service.Status().NextRetryAt
	fail.Store(false)
	poll("2", 3)
	poll("2", 3)
	poll("0", 3)
	poll("1", 4)
	cfg.BankedResetNotifications = false
	service.Configure(cfg)
	poll("3", 4)
}

func TestBankedObservationsRejectUnknownInvalidAndStaleCounts(t *testing.T) {
	now := time.Unix(1787279282, 0)
	for _, value := range []string{"", "-1", "1.5", "true", "null", "NaN", "+1", "9223372036854775808"} {
		account := observedAccount("codex", "50", now)
		account.Quota.Signals[bankedCountHeader] = value
		account.Quota.Signals["X-Codex-Credits-Balance"] = "100"
		if got := bankedObservations([]*auth.Auth{account}, now); len(got) != 0 {
			t.Fatalf("accepted %q", value)
		}
	}
	for _, edit := range []func(*auth.Auth){
		func(a *auth.Auth) { a.Disabled = true },
		func(a *auth.Auth) { a.Provider = "claude" },
		func(a *auth.Auth) { a.Quota.ObservedAt = now.Add(-ObservationMaxAge - time.Second) },
		func(a *auth.Auth) { a.Quota.ObservedAt = now.Add(time.Second) },
	} {
		account := observedAccount("codex", "50", now)
		account.Quota.Signals[bankedCountHeader] = "2"
		edit(account)
		if got := bankedObservations([]*auth.Auth{account}, now); len(got) != 0 {
			t.Fatal("accepted unavailable banked observation")
		}
	}
	account := observedAccount("codex", "50", now)
	account.Quota.Signals[bankedCountHeader] = "0"
	account.ModelStates = map[string]*auth.ModelState{"model": {Quota: auth.QuotaState{ObservedAt: now.Add(-time.Second), Signals: map[string]string{bankedCountHeader: "3"}}}}
	if got := bankedObservations([]*auth.Auth{account, account.Clone()}, now); len(got) != 1 || got[0].count != 0 {
		t.Fatalf("newest zero count not authoritative: %+v", got)
	}
}
