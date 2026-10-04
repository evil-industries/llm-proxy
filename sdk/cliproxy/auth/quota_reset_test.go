package auth

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func resetTestAuth(id string, now time.Time, after time.Duration) *Auth {
	return &Auth{ID: id, Provider: "codex", Quota: QuotaState{ObservedAt: now, Signals: map[string]string{
		"X-Codex-Primary-Used-Percent": "20",
		"X-Codex-Primary-Reset-At":     strconv.FormatInt(now.Add(after).Unix(), 10),
	}}}
}

func TestNextQuotaReset(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name   string
		mutate func(*Auth)
		want   time.Time
	}{
		{"absolute", func(a *Auth) {}, now.Add(time.Hour)},
		{"relative", func(a *Auth) {
			delete(a.Quota.Signals, "X-Codex-Primary-Reset-At")
			a.Quota.Signals["X-Codex-Primary-Reset-After-Seconds"] = "3600"
		}, now.Add(time.Hour)},
		{"claude", func(a *Auth) {
			a.Provider = "claude"
			a.Quota.Signals = map[string]string{"anthropic-ratelimit-unified-5h-utilization": "0.5", "anthropic-ratelimit-unified-5h-reset": strconv.FormatInt(now.Add(time.Hour).Unix(), 10)}
		}, now.Add(time.Hour)},
		{"stale", func(a *Auth) { a.Quota.ObservedAt = now.Add(-quotaResetObservationMaxAge) }, time.Time{}},
		{"future observation", func(a *Auth) { a.Quota.ObservedAt = now.Add(time.Second) }, time.Time{}},
		{"expired", func(a *Auth) { a.Quota.Signals["X-Codex-Primary-Reset-At"] = strconv.FormatInt(now.Unix(), 10) }, time.Time{}},
		{"invalid usage", func(a *Auth) { a.Quota.Signals["X-Codex-Primary-Used-Percent"] = "NaN" }, time.Time{}},
		{"invalid absolute does not fall back", func(a *Auth) {
			a.Quota.Signals["X-Codex-Primary-Reset-At"] = "invalid"
			a.Quota.Signals["X-Codex-Primary-Reset-After-Seconds"] = "3600"
		}, time.Time{}},
		{"exhausted independent window without reset", func(a *Auth) {
			a.Quota.Signals["X-Codex-Secondary-Used-Percent"] = "100"
		}, time.Time{}},
		{"exhausted independent window with malformed reset", func(a *Auth) {
			a.Quota.Signals["X-Codex-Secondary-Used-Percent"] = "100"
			a.Quota.Signals["X-Codex-Secondary-Reset-At"] = "unknown"
		}, time.Time{}},
		{"exhausted independent window", func(a *Auth) {
			a.Quota.Signals["X-Codex-Secondary-Used-Percent"] = "100"
			a.Quota.Signals["X-Codex-Secondary-Reset-At"] = strconv.FormatInt(now.Add(24*time.Hour).Unix(), 10)
		}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := resetTestAuth("a", now, time.Hour)
			tc.mutate(a)
			if got := nextQuotaReset(a, "", now); !got.Equal(tc.want) {
				t.Fatalf("reset = %v, want %v", got, tc.want)
			}
		})
	}
}

func queriedResetTestAuth(now time.Time) *Auth {
	a := resetTestAuth("account", now, time.Hour)
	a.AccountSnapshot = &AccountInfo{
		Quota:         a.Quota.Clone(),
		BankedResetAt: now.Add(15 * time.Minute),
	}
	a.AccountSnapshot.Quota.Signals["X-Codex-Secondary-Used-Percent"] = "30"
	a.AccountSnapshot.Quota.Signals["X-Codex-Secondary-Reset-At"] = strconv.FormatInt(now.Add(30*time.Minute).Unix(), 10)
	a.AccountSnapshot.Quota.Signals[codexResetCreditsCountHeader] = "2"
	return a
}

func TestNextQuotaResetUsesEarliestRegularOrBanked(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name   string
		mutate func(*Auth)
		want   time.Time
	}{
		{"banked", func(a *Auth) {}, now.Add(15 * time.Minute)},
		{"primary", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Primary-Reset-At"] = strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10)
		}, now.Add(5 * time.Minute)},
		{"secondary", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Secondary-Reset-At"] = strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10)
		}, now.Add(5 * time.Minute)},
		{"zero banked count", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals[codexResetCreditsCountHeader] = "0"
		}, now.Add(30 * time.Minute)},
		{"missing banked count", func(a *Auth) {
			delete(a.AccountSnapshot.Quota.Signals, codexResetCreditsCountHeader)
		}, now.Add(30 * time.Minute)},
		{"negative banked count", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals[codexResetCreditsCountHeader] = "-1"
		}, now.Add(30 * time.Minute)},
		{"fractional banked count", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals[codexResetCreditsCountHeader] = "1.5"
		}, now.Add(30 * time.Minute)},
		{"unknown banked expiry", func(a *Auth) {
			a.AccountSnapshot.BankedResetAt = time.Time{}
		}, now.Add(30 * time.Minute)},
		{"expired banked", func(a *Auth) {
			a.AccountSnapshot.BankedResetAt = now
		}, now.Add(30 * time.Minute)},
		{"stale query", func(a *Auth) {
			a.AccountSnapshot.Quota.ObservedAt = now.Add(-quotaResetObservationMaxAge)
		}, now.Add(time.Hour)},
		{"future query observation", func(a *Auth) {
			a.AccountSnapshot.Quota.ObservedAt = now.Add(time.Second)
		}, now.Add(time.Hour)},
		{"unavailable account", func(a *Auth) {
			a.Unavailable = true
		}, now.Add(30 * time.Minute)},
		{"exhausted independent window", func(a *Auth) {
			a.AccountSnapshot.Quota.Signals["X-Codex-Secondary-Used-Percent"] = "100"
		}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := queriedResetTestAuth(now)
			// Make the active query newer than the passive primary window.
			a.Quota.ObservedAt = now.Add(-time.Second)
			tc.mutate(a)
			if got := nextQuotaReset(a, "", now); !got.Equal(tc.want) {
				t.Fatalf("reset = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNextQuotaResetRetainsQueriedDeadlinesAfterPassiveReplacement(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    time.Time
	}{
		{"count only", http.Header{codexResetCreditsCountHeader: {"1"}}, now.Add(15 * time.Minute)},
		{"plan only", http.Header{"X-Codex-Plan-Type": {"pro"}}, now.Add(15 * time.Minute)},
		{"regular without reset", http.Header{"X-Codex-Primary-Used-Percent": {"40"}, codexResetCreditsCountHeader: {"0"}}, now.Add(30 * time.Minute)},
		{"new primary reset", http.Header{"X-Codex-Primary-Used-Percent": {"40"}, "X-Codex-Primary-Reset-At": {strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10)}}, now.Add(5 * time.Minute)},
		{"banked count cleared", http.Header{codexResetCreditsCountHeader: {"0"}}, now.Add(30 * time.Minute)},
		{"new exhaustion", http.Header{"X-Codex-Secondary-Used-Percent": {"100"}}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := queriedResetTestAuth(now)
			if !a.Quota.ObserveResponseHeadersForProvider("codex", tc.headers, now.Add(time.Second)) {
				t.Fatal("passive snapshot was not replaced")
			}
			if got := nextQuotaReset(a, "", now.Add(time.Second)); !got.Equal(tc.want) {
				t.Fatalf("reset = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNextQuotaResetQueryAndPassiveUseNewestWindow(t *testing.T) {
	now := time.Unix(1800000000, 0)
	a := queriedResetTestAuth(now)
	a.AccountSnapshot.Quota.Signals[codexResetCreditsCountHeader] = "0"
	a.Quota = resetTestAuth("", now.Add(-time.Minute), 5*time.Minute).Quota
	if got := nextQuotaReset(a, "", now); !got.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("older passive window overrode query: %v", got)
	}
	a.Quota = resetTestAuth("", now.Add(time.Second), 5*time.Minute).Quota
	if got := nextQuotaReset(a, "", now.Add(time.Second)); !got.Equal(now.Add(time.Second + 5*time.Minute)) {
		t.Fatalf("newer passive window did not override query: %v", got)
	}
	a.Quota.Signals["X-Codex-Primary-Reset-At"] = "invalid"
	if got := nextQuotaReset(a, "", now.Add(time.Second)); !got.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("invalid newer reset fell back to queried primary: %v", got)
	}
}

func TestQuotaResetSelectionAcrossStrategies(t *testing.T) {
	for _, strategy := range []string{"round-robin", "fill-first", "weighted", "affinity"} {
		t.Run(strategy, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			later := resetTestAuth("a-later", now, 2*time.Hour)
			sooner := resetTestAuth("z-sooner", now, time.Hour)
			var selector Selector = &RoundRobinSelector{}
			switch strategy {
			case "fill-first":
				selector = &FillFirstSelector{}
			case "weighted":
				selector = &WeightedRoundRobinSelector{}
			case "affinity":
				selector = NewSessionAffinitySelector(&RoundRobinSelector{})
			}
			if affinity, ok := selector.(*SessionAffinitySelector); ok {
				defer affinity.Stop()
			}
			m := NewManager(nil, selector, nil)
			opts := cliproxyexecutor.Options{Headers: map[string][]string{"Session-Id": {"quota-test"}}}
			// Bind affinity before the sooner-resetting account appears.
			ctx := selectorContextForAvailableAuths(context.Background(), selector, "")
			if _, err := selector.Pick(ctx, "codex", "", opts, []*Auth{later}); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				preferred, candidates, err := m.availableAuthsForSelector(selector, []*Auth{later, sooner}, "codex", "", now)
				if err != nil {
					t.Fatal(err)
				}
				ctx = selectorContextForAvailableAuths(context.Background(), selector, "", preferred)
				picked, err := selector.Pick(ctx, "codex", "", opts, candidates)
				want := sooner.ID
				if strategy == "affinity" {
					want = later.ID
				}
				if err != nil || picked.ID != want {
					t.Fatalf("picked %v, err %v", picked, err)
				}
			}
			sooner.Disabled = true
			_, candidates, err := m.availableAuthsForSelector(selector, []*Auth{later, sooner}, "codex", "", now)
			if err != nil || len(candidates) != 1 || candidates[0].ID != later.ID {
				t.Fatalf("disabled selection = %v, %v", candidates, err)
			}
		})
	}
}

func TestQuotaResetTiesAndUnknownFallback(t *testing.T) {
	now := time.Unix(1800000000, 0)
	a, b := resetTestAuth("a", now, time.Hour), resetTestAuth("b", now, time.Hour)
	model := func(*Auth) string { return "" }
	if got := preferSoonestQuotaReset([]*Auth{a, b}, now, model); len(got) != 2 {
		t.Fatal("ties must preserve rotation")
	}
	if got := preferSoonestQuotaReset([]*Auth{a, b}, now.Add(time.Hour), model); len(got) != 2 {
		t.Fatal("expired observations must fall back")
	}
	b.ModelStates = map[string]*ModelState{"model": {Quota: resetTestAuth("", now.Add(time.Second), time.Minute).Quota}}
	if got := nextQuotaReset(b, "model", now.Add(time.Second)); !got.Equal(now.Add(time.Second + time.Minute)) {
		t.Fatalf("model observation reset = %v", got)
	}
}

func TestManagerPrefersQuotaResetAndRetriesNextAccount(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.RegisterExecutor(&quotaAttemptIsolationExecutor{})
	for _, a := range []*Auth{resetTestAuth("later", now, 2*time.Hour), resetTestAuth("sooner", now, time.Hour)} {
		if _, err := m.Register(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	if m.useSchedulerFastPath() {
		t.Fatal("quota observations must not bypass reset ordering")
	}
	for _, mixed := range []bool{false, true} {
		for _, retry := range []bool{false, true} {
			tried := map[string]struct{}{}
			want := "sooner"
			if retry {
				tried[want] = struct{}{}
				want = "later"
			}
			var picked *Auth
			var err error
			if mixed {
				picked, _, _, err = m.pickNextMixed(context.Background(), []string{"codex"}, "", cliproxyexecutor.Options{}, tried)
			} else {
				picked, _, err = m.pickNext(context.Background(), "codex", "", cliproxyexecutor.Options{}, tried)
			}
			if err != nil {
				t.Fatal(err)
			}
			if picked.ID != want {
				t.Fatalf("mixed=%v retry=%v: picked %s, want %s", mixed, retry, picked.ID, want)
			}
		}
	}
}

func TestQuotaResetRespectsPriorityAndZeroWeight(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	a, b := resetTestAuth("sooner", now, time.Hour), resetTestAuth("later", now, 2*time.Hour)
	a.Attributes = map[string]string{AttributeWeight: "0"}
	m := NewManager(nil, &WeightedRoundRobinSelector{}, nil)
	_, candidates, err := m.availableAuthsForSelector(&WeightedRoundRobinSelector{}, []*Auth{a, b}, "codex", "", now)
	if err != nil || len(candidates) != 1 || candidates[0].ID != b.ID {
		t.Fatalf("zero-weight candidates = %v, %v", candidates, err)
	}
	a.Attributes = map[string]string{"priority": "1"}
	b.Attributes = map[string]string{"priority": "2"}
	_, candidates, err = m.availableAuthsForSelector(&RoundRobinSelector{}, []*Auth{a, b}, "codex", "", now)
	if err != nil || len(candidates) != 1 || candidates[0].ID != b.ID {
		t.Fatalf("priority candidates = %v, %v", candidates, err)
	}
}

func TestPluginDelegationKeepsQuotaResetPreference(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.RegisterExecutor(&quotaAttemptIsolationExecutor{})
	for _, a := range []*Auth{resetTestAuth("a-later", now, 2*time.Hour), resetTestAuth("z-sooner", now, time.Hour)} {
		if _, err := m.Register(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	m.SetPluginScheduler(&fakePluginScheduler{handled: true, resp: pluginapi.SchedulerPickResponse{Handled: true, DelegateBuiltin: pluginapi.SchedulerBuiltinRoundRobin}})
	for range 3 {
		picked, _, err := m.pickNext(context.Background(), "codex", "", cliproxyexecutor.Options{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if picked.ID != "z-sooner" {
			t.Fatalf("delegation bypassed preference: %s", picked.ID)
		}
	}
}

func TestQuotaResetAffinityNewThreadAndFailover(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	later, sooner := resetTestAuth("later", now, 2*time.Hour), resetTestAuth("sooner", now, time.Hour)
	selector := NewSessionAffinitySelector(&RoundRobinSelector{})
	defer selector.Stop()
	m := NewManager(nil, selector, nil)
	pick := func(session string) string {
		t.Helper()
		preferred, candidates, err := m.availableAuthsForSelector(selector, []*Auth{later, sooner}, "codex", "", now)
		if err != nil {
			t.Fatal(err)
		}
		ctx := selectorContextForAvailableAuths(context.Background(), selector, "", preferred)
		auth, err := selector.Pick(ctx, "codex", "", cliproxyexecutor.Options{Headers: map[string][]string{"Session-Id": {session}}}, candidates)
		if err != nil {
			t.Fatal(err)
		}
		return auth.ID
	}
	if got := pick("new-thread"); got != sooner.ID {
		t.Fatalf("cold binding = %s", got)
	}
	later.Quota = resetTestAuth("", now, time.Minute).Quota
	if got := pick("new-thread"); got != sooner.ID {
		t.Fatalf("binding moved with reset: %s", got)
	}
	if got := pick("another-thread"); got != later.ID {
		t.Fatalf("new thread = %s", got)
	}
	sooner.Disabled = true
	if got := pick("new-thread"); got != later.ID {
		t.Fatalf("failover = %s", got)
	}
}

func TestConcurrentThreadKeepsOneAccount(t *testing.T) {
	selector := NewSessionAffinitySelector(&RoundRobinSelector{})
	defer selector.Stop()
	auths := []*Auth{{ID: "one", Provider: "codex"}, {ID: "two", Provider: "codex"}}
	start := make(chan struct{})
	chosen := make(chan string, 64)
	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			auth, err := selector.Pick(context.Background(), "codex", "", cliproxyexecutor.Options{Headers: map[string][]string{"Session-Id": {"concurrent-thread"}}}, auths)
			if err != nil {
				t.Error(err)
				return
			}
			chosen <- auth.ID
		}()
	}
	close(start)
	workers.Wait()
	close(chosen)
	first := ""
	for id := range chosen {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("thread split across %s and %s", first, id)
		}
	}
}
