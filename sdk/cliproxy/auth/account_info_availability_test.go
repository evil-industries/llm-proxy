package auth

import (
	"strconv"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestQueriedAccountQuotaAvailability(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name   string
		mutate func(*Auth)
		block  bool
		reason blockReason
		after  time.Duration
	}{
		{"exhausted", func(*Auth) {}, true, blockReasonCooldown, 10 * time.Minute},
		{"both exhausted", func(a *Auth) { a.AccountSnapshot.Quota.Signals["X-Codex-Secondary-Used-Percent"] = "100" }, true, blockReasonCooldown, time.Hour},
		{"explicit allowed", func(a *Auth) { a.AccountSnapshot.Quota.Signals["X-Codex-Allowed"] = "true" }, false, blockReasonNone, 0},
		{"explicit rejected below full usage", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Allowed"] = "false"
			a.AccountSnapshot.Quota.Signals["X-Codex-Primary-Used-Percent"] = "0"
		}, true, blockReasonCooldown, time.Hour},
		{"explicit limit reached", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Limit-Reached"] = "true"
			a.AccountSnapshot.Quota.Signals["X-Codex-Primary-Used-Percent"] = "0"
		}, true, blockReasonCooldown, time.Hour},
		{"banked credits do not redeem themselves", func(a *Auth) {
			a.AccountSnapshot.BankedResetAt = now.Add(time.Minute)
			a.AccountSnapshot.Quota.Signals[codexResetCreditsCountHeader] = "2"
		}, true, blockReasonCooldown, 10 * time.Minute},
		{"new usage clears old rejection", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Allowed"] = "false"
			a.Quota = QuotaState{ObservedAt: now, Signals: map[string]string{"X-Codex-Primary-Used-Percent": "20"}}
		}, false, blockReasonNone, 0},
		{"new allowance clears old exhaustion", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Allowed"] = "false"
			a.Quota = QuotaState{ObservedAt: now, Signals: map[string]string{"X-Codex-Allowed": "true"}}
		}, false, blockReasonNone, 0},
		{"unrelated passive frame preserves rejection", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Allowed"] = "false"
			a.Quota = QuotaState{ObservedAt: now, Signals: map[string]string{codexResetCreditsCountHeader: "0"}}
		}, true, blockReasonCooldown, 10 * time.Minute},
		{"new exhaustion overrides old allowance", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Allowed"] = "true"
			a.Quota = QuotaState{ObservedAt: now, Signals: map[string]string{"X-Codex-Primary-Used-Percent": "100"}}
		}, true, blockReasonCooldown, 10 * time.Minute},
		{"new model usage clears rejection", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Allowed"] = "false"
			a.ModelStates = map[string]*ModelState{"model": {Quota: QuotaState{ObservedAt: now, Signals: map[string]string{"X-Codex-Primary-Used-Percent": "20"}}}}
		}, false, blockReasonNone, 0},
		{"expired reset", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Primary-Reset-At"] = strconv.FormatInt(now.Unix(), 10)
		}, false, blockReasonNone, 0},
		{"unknown reset", func(a *Auth) { delete(a.AccountSnapshot.Quota.Signals, "X-Codex-Primary-Reset-At") }, true, blockReasonOther, 0},
		{"stale query", func(a *Auth) { a.AccountSnapshot.Quota.ObservedAt = now.Add(-quotaResetObservationMaxAge) }, false, blockReasonNone, 0},
		{"passive only unchanged", func(a *Auth) { a.Quota = a.AccountSnapshot.Quota.Clone(); a.AccountSnapshot = nil }, false, blockReasonNone, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &Auth{Provider: "codex", AccountSnapshot: &AccountInfo{Quota: QuotaState{
				ObservedAt: now.Add(-time.Second), Signals: map[string]string{
					"X-Codex-Primary-Used-Percent":   "100",
					"X-Codex-Primary-Reset-At":       strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10),
					"X-Codex-Secondary-Used-Percent": "20",
					"X-Codex-Secondary-Reset-At":     strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
				},
			}}}
			tc.mutate(auth)
			blocked, reason, reset := isAuthBlockedForModel(auth, "model", now)
			wantReset := time.Time{}
			if tc.after > 0 {
				wantReset = now.Add(tc.after)
			}
			if blocked != tc.block || reason != tc.reason || !reset.Equal(wantReset) {
				t.Fatalf("availability = %v, %v, %v; want %v, %v, %v", blocked, reason, reset, tc.block, tc.reason, wantReset)
			}
		})
	}
}

func TestQueriedClaudeAccountStatusOverridesUtilization(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, status := range []string{"rejected", "allowed", "allowed_warning"} {
		t.Run(status, func(t *testing.T) {
			auth := &Auth{Provider: "claude", AccountSnapshot: &AccountInfo{Quota: QuotaState{
				ObservedAt: now, Signals: map[string]string{
					"Anthropic-Ratelimit-Unified-5h-Utilization": "1",
					"Anthropic-Ratelimit-Unified-5h-Status":      status,
					"Anthropic-Ratelimit-Unified-5h-Reset":       strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
				},
			}}}
			blocked, _, _ := isAuthBlockedForModel(auth, "model", now)
			if blocked != (status == "rejected") {
				t.Fatalf("status %q availability blocked = %v", status, blocked)
			}
		})
	}
}

func TestManagerDoesNotRouteQueriedExhaustedAccount(t *testing.T) {
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(schedulerTestExecutor{provider: "codex"})
	base, errRegister := manager.Register(t.Context(), &Auth{ID: "exhausted", Provider: "codex"})
	if errRegister != nil {
		t.Fatal(errRegister)
	}
	info := &AccountInfo{Metadata: map[string]any{"plan_type": "plus"}, Quota: QuotaState{
		ObservedAt: time.Now(), Signals: map[string]string{
			"X-Codex-Primary-Used-Percent": "100",
			"X-Codex-Primary-Reset-At":     strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
		},
	}}
	if errApply := manager.applyAccountInfo(t.Context(), base, info); errApply != nil {
		t.Fatal(errApply)
	}
	if picked, errPick := manager.SelectAuth(t.Context(), "codex", "", cliproxyexecutor.Options{}); picked != nil || errPick == nil {
		t.Fatalf("exhausted account routed: %v, %v", picked, errPick)
	}
}

func TestQueriedGraceAccountKeepsResetPreference(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name     string
		provider string
		signals  map[string]string
		banked   time.Time
		want     time.Time
	}{
		{
			name: "Codex regular", provider: "codex", want: now.Add(10 * time.Minute),
			signals: map[string]string{
				"X-Codex-Allowed":              "true",
				"X-Codex-Primary-Used-Percent": "100",
				"X-Codex-Primary-Reset-At":     strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10),
			},
		},
		{
			name: "Codex banked", provider: "codex", banked: now.Add(5 * time.Minute), want: now.Add(5 * time.Minute),
			signals: map[string]string{
				"X-Codex-Allowed":              "true",
				"X-Codex-Primary-Used-Percent": "100",
				"X-Codex-Primary-Reset-At":     strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10),
				codexResetCreditsCountHeader:   "1",
			},
		},
		{
			name: "Claude allowed warning", provider: "claude", want: now.Add(10 * time.Minute),
			signals: map[string]string{
				"Anthropic-Ratelimit-Unified-5h-Status":      "allowed_warning",
				"Anthropic-Ratelimit-Unified-5h-Utilization": "1",
				"Anthropic-Ratelimit-Unified-5h-Reset":       strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &Auth{Provider: tc.provider, AccountSnapshot: &AccountInfo{
				Quota: QuotaState{ObservedAt: now, Signals: tc.signals}, BankedResetAt: tc.banked,
			}}
			if got := nextQuotaReset(auth, "", now); !got.Equal(tc.want) {
				t.Fatalf("grace account reset preference = %v, want %v", got, tc.want)
			}
		})
	}
}
