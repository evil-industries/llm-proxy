package notifications

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestQuotaKeepsDistinctActivePools(t *testing.T) {
	now := time.Unix(1787279282, 0)
	standard := observedAccount("codex", "99", now.Add(-time.Minute))
	spark := observedAccount("codex", "2", now)
	spark.Quota.Signals["X-Codex-Active-Limit"] = "codex_bengalfox"
	standard.ModelStates = map[string]*auth.ModelState{"spark": {Quota: spark.Quota}}
	got := quotaObservations([]*auth.Auth{standard}, now)
	if len(got) != 2 {
		t.Fatalf("distinct pools collapsed: %+v", got)
	}
	remaining := map[string]float64{}
	for _, item := range got {
		remaining[item.window] = item.remaining
	}
	if math.Abs(remaining["primary"]-1) > 1e-9 || remaining["bengalfox-primary"] != 98 {
		t.Fatalf("pool measurements changed: %+v", got)
	}
}

func TestQuotaNormalizesExplicitPoolNamesAcrossTransports(t *testing.T) {
	now := time.Unix(1787279282, 0)
	httpAccount := observedAccount("codex", "99", now.Add(-time.Minute))
	httpAccount.Quota.Signals["X-Codex-Active-Limit"] = "codex_bengalfox"
	httpAccount.Quota.Signals["X-Codex-Bengalfox-Limit-Name"] = "GPT-5.3-Codex-Spark"
	httpAccount.Quota.Signals["X-Codex-Bengalfox-Secondary-Used-Percent"] = "90"
	wsAccount := observedAccount("codex", "81", now)
	wsAccount.Quota.Signals["X-Codex-Additional-Gpt-5.3-Codex-Spark-Limit-Name"] = "GPT-5.3-Codex-Spark"
	wsAccount.Quota.Signals["X-Codex-Additional-Gpt-5.3-Codex-Spark-Primary-Used-Percent"] = "2"
	wsAccount.Quota.Signals["X-Codex-Additional-Gpt-5.3-Codex-Spark-Secondary-Used-Percent"] = "3"
	got := quotaObservations([]*auth.Auth{httpAccount, wsAccount}, now)
	if len(got) != 3 {
		t.Fatalf("expected default and two named windows: %+v", got)
	}
	for _, item := range got {
		want, exists := map[string]float64{
			"primary": 19, "GPT-5.3-Codex-Spark primary": 98, "GPT-5.3-Codex-Spark secondary": 97,
		}[item.window]
		if !exists || math.Abs(item.remaining-want) > 1e-9 || !item.observed.Equal(now) {
			t.Fatalf("did not select newest measurement for pool/window: %+v", item)
		}
	}
}

func TestQuotaDoesNotGuessUnknownPoolAliases(t *testing.T) {
	now := time.Unix(1787279282, 0)
	a := observedAccount("codex", "99", now)
	a.Quota.Signals["X-Codex-Active-Limit"] = "codex_bengalfox"
	a.Quota.Signals["X-Codex-Additional-Gpt-5.3-Codex-Spark-Limit-Name"] = "GPT-5.3-Codex-Spark"
	a.Quota.Signals["X-Codex-Additional-Gpt-5.3-Codex-Spark-Primary-Used-Percent"] = "2"
	if got := quotaObservations([]*auth.Auth{a}, now); len(got) != 2 {
		t.Fatalf("unproven alias merged pools: %+v", got)
	}
}

func TestQuotaActivePoolDoesNotChangeNamespacedPoolIdentity(t *testing.T) {
	now := time.Unix(1787279282, 0)
	a := observedAccount("codex", "99", now)
	a.Quota.Signals["X-Codex-Active-Limit"] = "codex_bengalfox"
	a.Quota.Signals["X-Codex-Bengalfox-Primary-Used-Percent"] = "99"
	a.Quota.Signals["X-Codex-Code-Review-Primary-Used-Percent"] = "20"
	got := quotaObservations([]*auth.Auth{a}, now)
	if len(got) != 2 {
		t.Fatalf("active and matching namespaced windows did not deduplicate: %+v", got)
	}
	a.Quota.Signals["X-Codex-Active-Limit"] = "other"
	next := quotaObservations([]*auth.Auth{a}, now)
	if len(next) != 3 {
		t.Fatalf("active pool overrode namespaced identity: %+v", next)
	}
}

func TestQuotaPoolSwitchDoesNotSendFalseRecovery(t *testing.T) {
	titles := make(chan string, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		titles <- r.Header.Get("Title")
	}))
	defer server.Close()
	now := time.Unix(1787279282, 0)
	account := observedAccount("codex", "99", now)
	service := New(func() []*auth.Auth { return []*auth.Auth{account} })
	service.now = func() time.Time { return now }
	cfg := testSettings(server.URL)
	cfg.ResetNotifications = true
	service.Configure(cfg)
	poll := func(pool, used string, want ...string) {
		t.Helper()
		now = now.Add(time.Second)
		account.Quota.ObservedAt = now
		account.Quota.Signals["X-Codex-Active-Limit"] = pool
		account.Quota.Signals["X-Codex-Primary-Used-Percent"] = used
		service.poll(context.Background())
		if len(titles) != len(want) {
			t.Fatalf("pool=%q used=%s got %d notifications, want %v", pool, used, len(titles), want)
		}
		for _, expected := range want {
			if actual := <-titles; actual != expected {
				t.Fatalf("title = %q, want %q", actual, expected)
			}
		}
	}
	poll("", "99", "Critically low remaining quota")
	poll("codex_bengalfox", "2")
	poll("", "99")
	poll("codex_bengalfox", "99", "Critically low remaining quota")
	poll("", "10", "Quota recovered")
	poll("codex_bengalfox", "99")
	poll("codex_bengalfox", "10", "Quota recovered")
}
