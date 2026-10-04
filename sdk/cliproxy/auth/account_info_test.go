package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type accountInfoTestExecutor struct {
	schedulerTestExecutor
	fetch func(context.Context, *Auth) (*AccountInfo, error)
}

func (*accountInfoTestExecutor) SupportsAccountInfo(*Auth) bool { return true }

func (e *accountInfoTestExecutor) FetchAccountInfo(ctx context.Context, auth *Auth) (*AccountInfo, error) {
	return e.fetch(ctx, auth)
}

func accountInfoTestSnapshot(now time.Time, resetAfter time.Duration) *AccountInfo {
	return &AccountInfo{
		Quota: QuotaState{ObservedAt: now, Signals: map[string]string{
			"X-Codex-Primary-Used-Percent": "20",
			"X-Codex-Primary-Reset-At":     strconv.FormatInt(now.Add(resetAfter).Unix(), 10),
		}},
		Metadata: map[string]any{"plan_type": "plus"},
	}
}

func registerAccountInfoTestAuth(t *testing.T, manager *Manager, id string) *Auth {
	t.Helper()
	registered, errRegister := manager.Register(t.Context(), &Auth{
		ID: id, Provider: "codex", Status: StatusActive,
		Metadata: map[string]any{"access_token": "token", "email": "same@example.com"},
	})
	if errRegister != nil {
		t.Fatal(errRegister)
	}
	return registered
}

func TestManagerAccountInfoRequiredForRouting(t *testing.T) {
	for name, newSelector := range map[string]func() Selector{
		"round-robin":          func() Selector { return &RoundRobinSelector{} },
		"weighted-round-robin": func() Selector { return &WeightedRoundRobinSelector{} },
		"fill-first":           func() Selector { return &FillFirstSelector{} },
		"session-affinity":     func() Selector { return NewSessionAffinitySelector(&RoundRobinSelector{}) },
	} {
		for _, executorFirst := range []bool{false, true} {
			suffix := "/auth-first"
			if executorFirst {
				suffix = "/executor-first"
			}
			t.Run(name+suffix, func(t *testing.T) {
				selector := newSelector()
				if stoppable, ok := selector.(StoppableSelector); ok {
					t.Cleanup(stoppable.Stop)
				}
				manager := NewManager(nil, selector, nil)
				executor := &accountInfoTestExecutor{schedulerTestExecutor: schedulerTestExecutor{provider: "codex"}}
				if executorFirst {
					manager.RegisterExecutor(executor)
				}
				base := registerAccountInfoTestAuth(t, manager, "pending")
				if !executorFirst {
					manager.RegisterExecutor(executor)
				}
				manager.RegisterExecutor(schedulerTestExecutor{provider: "gemini"})
				for _, path := range []string{"single", "mixed", "pinned"} {
					assertPick := func(wantReady bool) {
						t.Helper()
						var picked *Auth
						var errPick error
						opts := cliproxyexecutor.Options{}
						if path == "pinned" {
							opts.Metadata = map[string]any{cliproxyexecutor.PinnedAuthMetadataKey: base.ID}
						}
						if path == "mixed" {
							picked, _, _, errPick = manager.pickNextMixed(t.Context(), []string{"codex", "gemini"}, "", opts, nil)
						} else {
							picked, errPick = manager.SelectAuth(t.Context(), "codex", "", opts)
						}
						if wantReady {
							if errPick != nil || picked == nil || picked.ID != base.ID {
								t.Fatalf("%s ready pick = %v, %v", path, picked, errPick)
							}
						} else if picked != nil || errPick == nil {
							t.Fatalf("%s pending account routed: %v, %v", path, picked, errPick)
						}
					}
					assertPick(false)
					if errApply := manager.applyAccountInfo(t.Context(), base, accountInfoTestSnapshot(time.Now(), time.Hour)); errApply != nil {
						t.Fatal(errApply)
					}
					assertPick(true)
					// Put the account back into its initial state for the next path.
					base = registerAccountInfoTestAuth(t, manager, base.ID)
				}
			})
		}
	}
}

func TestManagerAccountInfoPendingExcludedFromPluginCandidates(t *testing.T) {
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(&accountInfoTestExecutor{schedulerTestExecutor: schedulerTestExecutor{provider: "codex"}})
	registerAccountInfoTestAuth(t, manager, "pending")
	ready := registerAccountInfoTestAuth(t, manager, "ready")
	if errApply := manager.applyAccountInfo(t.Context(), ready, accountInfoTestSnapshot(time.Now(), time.Hour)); errApply != nil {
		t.Fatal(errApply)
	}
	scheduler := &fakePluginScheduler{handled: true, resp: pluginapi.SchedulerPickResponse{Handled: true, AuthID: ready.ID}}
	manager.SetPluginScheduler(scheduler)
	for _, mixed := range []bool{false, true} {
		var picked *Auth
		var errPick error
		if mixed {
			picked, _, _, errPick = manager.pickNextMixed(t.Context(), []string{"codex"}, "", cliproxyexecutor.Options{}, nil)
		} else {
			picked, errPick = manager.SelectAuth(t.Context(), "codex", "", cliproxyexecutor.Options{})
		}
		if errPick != nil || picked == nil || picked.ID != ready.ID {
			t.Fatalf("ready pick = %v, %v", picked, errPick)
		}
		request := scheduler.requests[len(scheduler.requests)-1]
		if len(request.Candidates) != 1 || request.Candidates[0].ID != ready.ID {
			t.Fatalf("plugin candidates = %#v, want only ready account", request.Candidates)
		}
	}
}

func TestManagerAccountInfoPublishesPlanForRouting(t *testing.T) {
	for _, previousPlan := range []string{"", "pro"} {
		t.Run("previous-plan="+previousPlan, func(t *testing.T) {
			manager := NewManager(nil, &RoundRobinSelector{}, nil)
			manager.RegisterExecutor(&accountInfoTestExecutor{schedulerTestExecutor: schedulerTestExecutor{provider: "codex"}})
			base, errRegister := manager.Register(t.Context(), &Auth{
				ID: "account", Provider: "codex", Status: StatusActive,
				Attributes: map[string]string{"plan_type": previousPlan},
				Metadata:   map[string]any{"access_token": "token"},
			})
			if errRegister != nil {
				t.Fatal(errRegister)
			}
			info := accountInfoTestSnapshot(time.Now(), time.Hour)
			info.Metadata["plan_type"] = "free"
			if errApply := manager.applyAccountInfo(t.Context(), base, info); errApply != nil {
				t.Fatal(errApply)
			}
			current, _ := manager.GetByID(base.ID)
			if current.Attributes["plan_type"] != "free" || !isFreeCodexAuth(current) {
				t.Fatalf("queried free plan was not published for routing: %#v", current.Attributes)
			}
			if picked, errPick := manager.SelectAuth(t.Context(), "codex", "", cliproxyexecutor.Options{}); errPick != nil || picked == nil {
				t.Fatalf("ordinary request could not use queried free account: %v, %v", picked, errPick)
			}
			opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.DisallowFreeAuthMetadataKey: true}}
			if picked, errPick := manager.SelectAuth(t.Context(), "codex", "", opts); picked != nil || errPick == nil {
				t.Fatalf("paid-only request routed to queried free account: %v, %v", picked, errPick)
			}
		})
	}
	t.Run("preserves concurrent user attribute change", func(t *testing.T) {
		manager := NewManager(nil, &RoundRobinSelector{}, nil)
		base := registerAccountInfoTestAuth(t, manager, "account")
		updated := base.Clone()
		updated.Attributes = map[string]string{"plan_type": "enterprise"}
		if _, errUpdate := manager.Update(t.Context(), updated); errUpdate != nil {
			t.Fatal(errUpdate)
		}
		info := accountInfoTestSnapshot(time.Now(), time.Hour)
		info.Metadata["plan_type"] = "free"
		if errApply := manager.applyAccountInfo(t.Context(), base, info); errApply != nil {
			t.Fatal(errApply)
		}
		current, _ := manager.GetByID(base.ID)
		if current.Attributes["plan_type"] != "enterprise" {
			t.Fatalf("query overwrote concurrent plan attribute: %#v", current.Attributes)
		}
	})
}

func TestManagerAccountInfoRejectsIncompleteQueries(t *testing.T) {
	now := time.Now()
	for name, mutate := range map[string]func(*AccountInfo) *AccountInfo{
		"nil":           func(*AccountInfo) *AccountInfo { return nil },
		"unobserved":    func(info *AccountInfo) *AccountInfo { info.Quota.ObservedAt = time.Time{}; return info },
		"missing quota": func(info *AccountInfo) *AccountInfo { info.Quota.Signals = nil; return info },
		"missing metadata": func(info *AccountInfo) *AccountInfo {
			info.Metadata = nil
			return info
		},
		"plan only": func(info *AccountInfo) *AccountInfo {
			info.Quota.Signals = map[string]string{"X-Codex-Plan-Type": "plus"}
			return info
		},
		"usage only": func(info *AccountInfo) *AccountInfo {
			delete(info.Quota.Signals, "X-Codex-Primary-Reset-At")
			return info
		},
		"invalid reset": func(info *AccountInfo) *AccountInfo {
			info.Quota.Signals["X-Codex-Primary-Reset-At"] = "invalid"
			return info
		},
	} {
		t.Run(name, func(t *testing.T) {
			manager := NewManager(nil, &RoundRobinSelector{}, nil)
			manager.RegisterExecutor(&accountInfoTestExecutor{schedulerTestExecutor: schedulerTestExecutor{provider: "codex"}})
			base := registerAccountInfoTestAuth(t, manager, "pending")
			if errApply := manager.applyAccountInfo(t.Context(), base, mutate(accountInfoTestSnapshot(now, time.Hour))); errApply == nil {
				t.Fatal("incomplete query accepted")
			}
			current, _ := manager.GetByID(base.ID)
			if current.AccountSnapshot != nil {
				t.Fatal("incomplete query installed a snapshot")
			}
			if picked, errPick := manager.SelectAuth(t.Context(), "codex", "", cliproxyexecutor.Options{}); picked != nil || errPick == nil {
				t.Fatalf("incomplete query made account routable: %v, %v", picked, errPick)
			}
		})
	}
	t.Run("exhausted but complete", func(t *testing.T) {
		manager := NewManager(nil, &RoundRobinSelector{}, nil)
		base := registerAccountInfoTestAuth(t, manager, "exhausted")
		info := accountInfoTestSnapshot(now, time.Hour)
		info.Quota.Signals["X-Codex-Primary-Used-Percent"] = "100"
		if errApply := manager.applyAccountInfo(t.Context(), base, info); errApply != nil {
			t.Fatalf("complete exhausted query rejected: %v", errApply)
		}
	})
}

type accountInfoTestResult struct {
	info *AccountInfo
	err  error
}

type accountInfoTestQuery struct {
	auth   *Auth
	result chan accountInfoTestResult
}

func newAccountInfoTestLoop(t *testing.T, now time.Time) (*Manager, *accountInfoRefreshLoop, chan *accountInfoTestQuery, func(time.Time)) {
	t.Helper()
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	queries := make(chan *accountInfoTestQuery, 32)
	executor := &accountInfoTestExecutor{schedulerTestExecutor: schedulerTestExecutor{provider: "codex"}}
	executor.fetch = func(ctx context.Context, auth *Auth) (*AccountInfo, error) {
		query := &accountInfoTestQuery{auth: auth, result: make(chan accountInfoTestResult, 1)}
		select {
		case queries <- query:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case result := <-query.result:
			return result.info, result.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(executor)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	loop := &accountInfoRefreshLoop{
		manager: manager, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), attempts: make(map[string]*accountInfoAttempt),
		slots:   make(chan struct{}, accountInfoConcurrency),
		nowFunc: func() time.Time { return time.Unix(0, clock.Load()) },
	}
	return manager, loop, queries, func(now time.Time) { clock.Store(now.UnixNano()) }
}

func waitAccountInfoQuery(t *testing.T, queries <-chan *accountInfoTestQuery) *accountInfoTestQuery {
	t.Helper()
	select {
	case query := <-queries:
		return query
	case <-time.After(5 * time.Second):
		t.Fatal("account information query did not start")
		return nil
	}
}

func finishAccountInfoQuery(t *testing.T, loop *accountInfoRefreshLoop, query *accountInfoTestQuery, result accountInfoTestResult) {
	t.Helper()
	query.result <- result
	select {
	case <-loop.wake:
	case <-time.After(5 * time.Second):
		t.Fatal("account information query did not finish")
	}
}

func accountInfoAttemptSnapshot(loop *accountInfoRefreshLoop, id string) accountInfoAttempt {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return *loop.attempts[id]
}

func TestAccountInfoRefreshRetriesWithCappedBackoff(t *testing.T) {
	now := time.Unix(1800000000, 0)
	manager, loop, queries, setNow := newAccountInfoTestLoop(t, now)
	base := registerAccountInfoTestAuth(t, manager, "retry")
	wantDelays := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
	for index, wantDelay := range wantDelays {
		loop.poll()
		query := waitAccountInfoQuery(t, queries)
		// Repeated polls cannot duplicate an active query.
		loop.poll()
		if len(queries) != 0 || len(loop.slots) != 1 {
			t.Fatal("duplicate account information query while in flight")
		}
		result := accountInfoTestResult{err: errors.New("temporary query failure")}
		if index%2 == 1 {
			result = accountInfoTestResult{info: &AccountInfo{Quota: accountInfoTestSnapshot(now, time.Hour).Quota}}
		}
		finishAccountInfoQuery(t, loop, query, result)
		attempt := accountInfoAttemptSnapshot(loop, base.ID)
		if attempt.inFlight || attempt.failures != uint(index+1) || !attempt.nextAttempt.Equal(now.Add(wantDelay)) {
			t.Fatalf("attempt %d = %#v, want next attempt %v", index+1, attempt, now.Add(wantDelay))
		}
		current, _ := manager.GetByID(base.ID)
		if current.AccountSnapshot != nil {
			t.Fatal("failed or partial query initialized account")
		}
		setNow(attempt.nextAttempt.Add(-time.Nanosecond))
		loop.poll()
		if accountInfoAttemptSnapshot(loop, base.ID).inFlight {
			t.Fatal("query started before retry deadline")
		}
		now = attempt.nextAttempt
		setNow(now)
	}
	loop.poll()
	query := waitAccountInfoQuery(t, queries)
	finishAccountInfoQuery(t, loop, query, accountInfoTestResult{info: accountInfoTestSnapshot(now, time.Hour)})
	attempt := accountInfoAttemptSnapshot(loop, base.ID)
	if attempt.failures != 0 || !attempt.nextAttempt.Equal(now.Add(accountInfoRefreshInterval)) {
		t.Fatalf("successful retry did not reset backoff: %#v", attempt)
	}
	current, _ := manager.GetByID(base.ID)
	if current.AccountSnapshot == nil {
		t.Fatal("successful retry did not initialize account")
	}
	now = attempt.nextAttempt
	setNow(now)
	loop.poll()
	query = waitAccountInfoQuery(t, queries)
	finishAccountInfoQuery(t, loop, query, accountInfoTestResult{err: errors.New("temporary refresh failure")})
	attempt = accountInfoAttemptSnapshot(loop, base.ID)
	current, _ = manager.GetByID(base.ID)
	if attempt.failures != 1 || !attempt.nextAttempt.Equal(now.Add(5*time.Second)) || current.AccountSnapshot == nil {
		t.Fatal("failed periodic refresh revoked successful query or retained old backoff")
	}
}

func TestAccountInfoRefreshSchedulesResetAndNewAccounts(t *testing.T) {
	for _, resetAfter := range []time.Duration{time.Hour, 2 * time.Minute} {
		t.Run(resetAfter.String(), func(t *testing.T) {
			now := time.Unix(1800000000, 0)
			manager, loop, queries, setNow := newAccountInfoTestLoop(t, now)
			base := registerAccountInfoTestAuth(t, manager, "first")
			loop.poll()
			query := waitAccountInfoQuery(t, queries)
			finishAccountInfoQuery(t, loop, query, accountInfoTestResult{info: accountInfoTestSnapshot(now, resetAfter)})
			wantNext := now.Add(min(resetAfter, accountInfoRefreshInterval))
			if got := accountInfoAttemptSnapshot(loop, base.ID).nextAttempt; !got.Equal(wantNext) {
				t.Fatalf("next refresh = %v, want %v", got, wantNext)
			}
			registerAccountInfoTestAuth(t, manager, "new")
			loop.poll()
			query = waitAccountInfoQuery(t, queries)
			if query.auth.ID != "new" {
				t.Fatalf("new-account poll queried %q", query.auth.ID)
			}
			finishAccountInfoQuery(t, loop, query, accountInfoTestResult{info: accountInfoTestSnapshot(now, time.Hour)})
			setNow(wantNext)
			loop.poll()
			query = waitAccountInfoQuery(t, queries)
			// A five-minute refresh also makes the newly registered account due.
			queriedFirst := query.auth.ID == base.ID
			finishAccountInfoQuery(t, loop, query, accountInfoTestResult{info: accountInfoTestSnapshot(wantNext, time.Hour)})
			if len(queries) > 0 || len(loop.slots) > 0 {
				query = waitAccountInfoQuery(t, queries)
				queriedFirst = queriedFirst || query.auth.ID == base.ID
				finishAccountInfoQuery(t, loop, query, accountInfoTestResult{info: accountInfoTestSnapshot(wantNext, time.Hour)})
			}
			if !queriedFirst {
				t.Fatal("initialized account was not refreshed when due")
			}
		})
	}
}

func TestAccountInfoRefreshFencesReplacedCredentials(t *testing.T) {
	for _, change := range []string{"re-register", "token", "identity", "identity deletion"} {
		t.Run(change, func(t *testing.T) {
			now := time.Unix(1800000000, 0)
			manager, loop, queries, _ := newAccountInfoTestLoop(t, now)
			base := registerAccountInfoTestAuth(t, manager, "account")
			loop.poll()
			stale := waitAccountInfoQuery(t, queries)
			if change == "re-register" {
				manager.Remove(t.Context(), base.ID)
				registerAccountInfoTestAuth(t, manager, base.ID)
			} else {
				updated, _ := manager.GetByID(base.ID)
				if change == "token" {
					updated.Metadata["access_token"] = "new-token"
				} else if change == "identity deletion" {
					delete(updated.Metadata, "email")
				} else {
					updated.Metadata["email"] = "new@example.com"
				}
				if _, errUpdate := manager.Update(t.Context(), updated); errUpdate != nil {
					t.Fatal(errUpdate)
				}
			}
			finishAccountInfoQuery(t, loop, stale, accountInfoTestResult{info: accountInfoTestSnapshot(now, time.Hour)})
			current, _ := manager.GetByID(base.ID)
			if current.AccountSnapshot != nil {
				t.Fatal("stale query initialized replaced credentials")
			}
			loop.poll()
			fresh := waitAccountInfoQuery(t, queries)
			if fresh.auth.RegistrationEpoch != current.RegistrationEpoch || CredentialsChanged(fresh.auth, current) || accountIdentityChanged(fresh.auth, current) {
				t.Fatal("replacement query used stale credentials")
			}
			finishAccountInfoQuery(t, loop, fresh, accountInfoTestResult{info: accountInfoTestSnapshot(now, time.Hour)})
			current, _ = manager.GetByID(base.ID)
			if current.AccountSnapshot == nil {
				t.Fatal("fresh query did not initialize replacement")
			}
		})
	}
}

func TestManagerAccountInfoSnapshotPreservedForSameAccount(t *testing.T) {
	for _, change := range []string{"watcher", "oauth refresh", "watcher token refresh", "new identity", "unidentified replacement", "identity deletion"} {
		t.Run(change, func(t *testing.T) {
			manager := NewManager(nil, &RoundRobinSelector{}, nil)
			manager.RegisterExecutor(&accountInfoTestExecutor{schedulerTestExecutor: schedulerTestExecutor{provider: "codex"}})
			base := registerAccountInfoTestAuth(t, manager, "account")
			if errApply := manager.applyAccountInfo(t.Context(), base, accountInfoTestSnapshot(time.Now(), time.Hour)); errApply != nil {
				t.Fatal(errApply)
			}
			base, _ = manager.GetByID(base.ID)
			updated := base.Clone()
			updated.AccountSnapshot = nil
			updated.Metadata["notes"] = "updated by watcher"
			if change != "watcher" && change != "identity deletion" {
				updated.Metadata["access_token"] = "new-token"
			}
			if change == "new identity" {
				updated.Metadata["email"] = "different@example.com"
			} else if change == "unidentified replacement" || change == "identity deletion" {
				delete(updated.Metadata, "email")
			}
			var current *Auth
			var errUpdate error
			if change == "oauth refresh" {
				current, errUpdate = manager.UpdateRefreshedAuth(t.Context(), base, updated)
			} else {
				current, errUpdate = manager.Update(t.Context(), updated)
			}
			if errUpdate != nil {
				t.Fatal(errUpdate)
			}
			wantSnapshot := change != "new identity" && change != "unidentified replacement" && change != "identity deletion"
			if (current.AccountSnapshot != nil) != wantSnapshot {
				t.Fatalf("snapshot present = %v, want %v", current.AccountSnapshot != nil, wantSnapshot)
			}
		})
	}
}

func TestAccountInfoSnapshotRuntimeOnlyAndCloneIsolation(t *testing.T) {
	auth := &Auth{ID: "account", Provider: "codex", AccountSnapshot: accountInfoTestSnapshot(time.Now(), time.Hour)}
	cloned := auth.Clone()
	cloned.AccountSnapshot.Metadata["plan_type"] = "changed"
	cloned.AccountSnapshot.Quota.Signals["X-Codex-Primary-Used-Percent"] = "99"
	if auth.AccountSnapshot.Metadata["plan_type"] != "plus" || auth.AccountSnapshot.Quota.Signals["X-Codex-Primary-Used-Percent"] != "20" {
		t.Fatal("account snapshot clone shares mutable maps")
	}
	payload, errMarshal := json.Marshal(auth)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	if strings.Contains(string(payload), "plan_type") || strings.Contains(string(payload), "AccountSnapshot") || strings.Contains(string(payload), "account_info") {
		t.Fatalf("runtime snapshot serialized: %s", payload)
	}
	registeredManager := NewManager(nil, &RoundRobinSelector{}, nil)
	registeredManager.RegisterExecutor(&accountInfoTestExecutor{schedulerTestExecutor: schedulerTestExecutor{provider: "codex"}})
	registered, errRegister := registeredManager.Register(t.Context(), auth.Clone())
	if errRegister != nil {
		t.Fatal(errRegister)
	}
	if registered.AccountSnapshot != nil || !registered.accountInfoRequired {
		t.Fatal("new registration retained previous query readiness")
	}
	manager := NewManager(&schedulerLoadStore{auths: []*Auth{auth}}, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(&accountInfoTestExecutor{schedulerTestExecutor: schedulerTestExecutor{provider: "codex"}})
	if errLoad := manager.Load(t.Context()); errLoad != nil {
		t.Fatal(errLoad)
	}
	loaded, _ := manager.GetByID(auth.ID)
	if loaded.AccountSnapshot != nil || !loaded.accountInfoRequired {
		t.Fatal("loaded credentials retained query readiness")
	}
}
