package notifications

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func observedAccount(provider, used string, now time.Time) *auth.Auth {
	name := "X-Codex-Primary-Used-Percent"
	if provider == "claude" {
		name = "Anthropic-Ratelimit-Unified-5h-Utilization"
	}
	return &auth.Auth{ID: "credential", Provider: provider, Label: "Operations", Quota: auth.QuotaState{
		ObservedAt: now, Signals: map[string]string{name: used},
	}}
}

func TestQuotaPercentUnitsAndNamespaces(t *testing.T) {
	now := time.Unix(1787279282, 0)
	codex := observedAccount("codex", "81", now)
	codex.Quota.Signals["X-Codex-Primary-Reset-At"] = "1787588999"
	codex.Quota.Signals["X-Codex-Bengalfox-Secondary-Used-Percent"] = "99"
	claude := observedAccount("claude", "0.96", now)
	claude.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Reset"] = "1787296800"
	claude.Quota.Signals["Anthropic-Ratelimit-Unified-Fallback-Percentage"] = "0.9"
	got := quotaObservations([]*auth.Auth{codex, claude}, now)
	if len(got) != 3 {
		t.Fatalf("observations = %+v", got)
	}
	remaining := make(map[string]float64)
	for _, item := range got {
		remaining[item.provider+"/"+item.window] = item.remaining
	}
	for key, want := range map[string]float64{"codex/primary": 19, "codex/bengalfox-secondary": 1, "claude/5h": 4} {
		if math.Abs(remaining[key]-want) > 1e-9 {
			t.Errorf("%s remaining = %v, want %v", key, remaining[key], want)
		}
	}
}

func TestQuotaIgnoresInvalidAndUnavailableObservations(t *testing.T) {
	now := time.Unix(1787279282, 0)
	for _, tc := range []struct {
		name string
		edit func(*auth.Auth)
	}{
		{"disabled", func(a *auth.Auth) { a.Disabled = true }},
		{"disabled status", func(a *auth.Auth) { a.Status = auth.StatusDisabled }},
		{"stale", func(a *auth.Auth) { a.Quota.ObservedAt = now.Add(-ObservationMaxAge - time.Nanosecond) }},
		{"future", func(a *auth.Auth) { a.Quota.ObservedAt = now.Add(time.Nanosecond) }},
		{"unobserved", func(a *auth.Auth) { a.Quota.ObservedAt = time.Time{} }},
		{"expired reset", func(a *auth.Auth) { a.Quota.Signals["X-Codex-Primary-Reset-At"] = fmt.Sprint(now.Unix()) }},
		{"invalid reset", func(a *auth.Auth) { a.Quota.Signals["X-Codex-Primary-Reset-At"] = "invalid" }},
		{"expired relative reset", func(a *auth.Auth) {
			a.Quota.ObservedAt = now.Add(-time.Minute)
			a.Quota.Signals["X-Codex-Primary-Reset-After-Seconds"] = "30"
		}},
		{"unsupported provider", func(a *auth.Auth) { a.Provider = "devin" }},
		{"cooldown only", func(a *auth.Auth) {
			a.Quota.Signals = nil
			a.Quota.Exceeded = true
			a.Quota.NextRecoverAt = now.Add(time.Hour)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := observedAccount("codex", "99", now)
			tc.edit(a)
			if got := quotaObservations([]*auth.Auth{a}, now); len(got) != 0 {
				t.Fatalf("unexpected observations: %+v", got)
			}
		})
	}
	for _, provider := range []string{"codex", "claude"} {
		for _, value := range []string{"NaN", "+Inf", "-1", "101", "invalid"} {
			if got := quotaObservations([]*auth.Auth{observedAccount(provider, value, now)}, now); len(got) != 0 {
				t.Fatalf("accepted %s utilization %s", provider, value)
			}
		}
	}
	if got := quotaObservations([]*auth.Auth{observedAccount("claude", "95", now)}, now); len(got) != 0 {
		t.Fatal("interpreted Claude utilization as a percent")
	}
}

func TestQuotaDeduplicatesAccountAndUsesNewestSnapshot(t *testing.T) {
	now := time.Unix(1787279282, 0)
	old := observedAccount("codex", "99", now.Add(-time.Minute))
	old.Metadata = map[string]any{"account_id": "same-account"}
	newer := observedAccount("codex", "20", now)
	newer.ID = "other-credential"
	newer.Metadata = map[string]any{"account_id": "same-account"}
	got := quotaObservations([]*auth.Auth{old, newer, nil}, now)
	if len(got) != 1 || got[0].remaining != 80 {
		t.Fatalf("observations = %+v", got)
	}
}

func TestQuotaReadsModelSnapshotsWithoutDuplicatingAccountWindow(t *testing.T) {
	now := time.Unix(1787279282, 0)
	account := observedAccount("codex", "20", now)
	modelQuota := observedAccount("codex", "99", now.Add(-time.Minute)).Quota
	account.ModelStates = map[string]*auth.ModelState{"model": {Quota: modelQuota}}
	got := quotaObservations([]*auth.Auth{account}, now)
	if len(got) != 1 || got[0].remaining != 80 {
		t.Fatalf("older model snapshot overrode current account observation: %+v", got)
	}
	account.Quota = auth.QuotaState{}
	got = quotaObservations([]*auth.Auth{account}, now)
	if len(got) != 1 || math.Abs(got[0].remaining-1) > 1e-9 {
		t.Fatalf("model-only observation missing: %+v", got)
	}
}
