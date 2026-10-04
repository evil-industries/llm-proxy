package auth

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementevents"
	log "github.com/sirupsen/logrus"
)

const (
	accountInfoPollInterval    = 5 * time.Second
	accountInfoRefreshInterval = 5 * time.Minute
	accountInfoMaxBackoff      = 5 * time.Minute
	accountInfoConcurrency     = 8
)

// AccountInfo is a successful account query, separate from passive quota frames.
// BankedResetAt is the earliest expiry of an available banked reset; querying
// account information never redeems a reset credit.
type AccountInfo struct {
	Quota         QuotaState
	Metadata      map[string]any
	BankedResetAt time.Time
}

func (info *AccountInfo) Clone() *AccountInfo {
	if info == nil {
		return nil
	}
	cloned := *info
	cloned.Quota = info.Quota.Clone()
	cloned.Metadata = make(map[string]any, len(info.Metadata))
	for key, value := range info.Metadata {
		cloned.Metadata[key] = value
	}
	return &cloned
}

// AccountInfoFetcher is implemented only for credentials with an account query
// endpoint. Such credentials cannot route until their first query succeeds.
type AccountInfoFetcher interface {
	SupportsAccountInfo(*Auth) bool
	FetchAccountInfo(context.Context, *Auth) (*AccountInfo, error)
}

type accountInfoAttempt struct {
	auth        *Auth
	failures    uint
	nextAttempt time.Time
	inFlight    bool
}

type accountInfoRefreshLoop struct {
	manager  *Manager
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	mu       sync.Mutex
	attempts map[string]*accountInfoAttempt
	slots    chan struct{}
	nowFunc  func() time.Time
}

// StartAccountInfoRefresh initializes accounts in the background and keeps their
// queried resets current. Failed queries retry indefinitely with capped backoff.
func (m *Manager) StartAccountInfoRefresh(ctx context.Context) {
	if m == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	loopCtx, cancel := context.WithCancel(ctx)
	loop := &accountInfoRefreshLoop{
		manager: m, ctx: loopCtx, cancel: cancel, wake: make(chan struct{}, 1),
		attempts: make(map[string]*accountInfoAttempt), slots: make(chan struct{}, accountInfoConcurrency), nowFunc: time.Now,
	}
	m.mu.Lock()
	previous := m.accountInfoLoop
	m.accountInfoLoop = loop
	m.mu.Unlock()
	if previous != nil {
		previous.cancel()
	}
	go loop.run()
}

func (m *Manager) StopAccountInfoRefresh() {
	if m == nil {
		return
	}
	m.mu.Lock()
	loop := m.accountInfoLoop
	m.accountInfoLoop = nil
	m.mu.Unlock()
	if loop != nil {
		loop.cancel()
	}
}

func (m *Manager) wakeAccountInfoRefresh() {
	m.mu.RLock()
	loop := m.accountInfoLoop
	m.mu.RUnlock()
	if loop != nil {
		loop.notify()
	}
}

func (loop *accountInfoRefreshLoop) notify() {
	select {
	case loop.wake <- struct{}{}:
	default:
	}
}

func (loop *accountInfoRefreshLoop) run() {
	ticker := time.NewTicker(accountInfoPollInterval)
	defer ticker.Stop()
	for loop.ctx.Err() == nil {
		loop.poll()
		select {
		case <-loop.ctx.Done():
			return
		case <-ticker.C:
		case <-loop.wake:
		}
	}
}

func (loop *accountInfoRefreshLoop) poll() {
	now := loop.nowFunc()
	loop.manager.mu.RLock()
	queries := make(map[string]AccountInfoFetcher)
	auths := make([]*Auth, 0, len(loop.manager.auths))
	for _, auth := range loop.manager.auths {
		if auth == nil || auth.Disabled || auth.Status == StatusDisabled {
			continue
		}
		fetcher, ok := loop.manager.executors[executorKeyFromAuth(auth)].(AccountInfoFetcher)
		if !ok || !fetcher.SupportsAccountInfo(auth) {
			continue
		}
		auths = append(auths, auth.Clone())
		queries[auth.ID] = fetcher
	}
	loop.manager.mu.RUnlock()
	loop.mu.Lock()
	defer loop.mu.Unlock()
	for id, attempt := range loop.attempts {
		if queries[id] == nil && !attempt.inFlight {
			delete(loop.attempts, id)
		}
	}
	for _, auth := range auths {
		attempt := loop.attempts[auth.ID]
		if attempt != nil && attempt.inFlight {
			continue
		}
		if attempt == nil || attempt.auth.RegistrationEpoch != auth.RegistrationEpoch || CredentialsChanged(attempt.auth, auth) || accountIdentityChanged(attempt.auth, auth) {
			attempt = &accountInfoAttempt{auth: auth}
			loop.attempts[auth.ID] = attempt
		}
		if now.Before(attempt.nextAttempt) || loop.ctx.Err() != nil {
			continue
		}
		select {
		case loop.slots <- struct{}{}:
		default:
			return
		}
		attempt.auth = auth
		attempt.inFlight = true
		go loop.query(attempt, queries[auth.ID])
	}
}

func (loop *accountInfoRefreshLoop) query(attempt *accountInfoAttempt, fetcher AccountInfoFetcher) {
	base := attempt.auth
	info, errFetch := fetcher.FetchAccountInfo(loop.ctx, base.Clone())
	if errFetch == nil {
		errFetch = loop.manager.applyAccountInfo(loop.ctx, base, info)
	}
	now := loop.nowFunc()
	loop.mu.Lock()
	attempt.inFlight = false
	if errFetch != nil {
		attempt.failures++
		attempt.nextAttempt = now.Add(accountInfoRetryDelay(attempt.failures))
	} else {
		attempt.failures = 0
		attempt.nextAttempt = now.Add(accountInfoRefreshInterval)
		if reset := nextQuotaReset(&Auth{Provider: base.Provider, AccountSnapshot: info}, "", now); reset.After(now) && reset.Before(attempt.nextAttempt) {
			attempt.nextAttempt = reset
		}
	}
	loop.mu.Unlock()
	<-loop.slots
	if errFetch != nil && loop.ctx.Err() == nil {
		log.WithFields(log.Fields{"provider": base.Provider, "auth_id": base.ID}).Debug("account information query failed; scheduled retry")
	}
	loop.notify()
}

func accountInfoRetryDelay(failures uint) time.Duration {
	delay := accountInfoPollInterval
	for failures > 1 && delay < accountInfoMaxBackoff {
		delay *= 2
		failures--
	}
	return min(delay, accountInfoMaxBackoff)
}

func (m *Manager) requiresAccountInfoLocked(auth *Auth) bool {
	fetcher, ok := m.executors[executorKeyFromAuth(auth)].(AccountInfoFetcher)
	return ok && fetcher.SupportsAccountInfo(auth)
}

func accountIdentityChanged(before, after *Auth) bool {
	if !strings.EqualFold(before.Provider, after.Provider) {
		return true
	}
	for _, key := range []string{"account_id", "account_uuid", "organization_uuid", "email"} {
		left, right := authMetadataString(before, key), authMetadataString(after, key)
		if left != right {
			return true
		}
	}
	return false
}

func accountIdentityMatches(before, after *Auth) bool {
	for _, key := range []string{"account_id", "account_uuid", "email"} {
		left, right := authMetadataString(before, key), authMetadataString(after, key)
		if left != "" && left == right {
			return true
		}
	}
	return false
}

func (m *Manager) applyAccountInfo(ctx context.Context, base *Auth, info *AccountInfo) error {
	if !completeAccountInfo(base.Provider, info) {
		return fmt.Errorf("incomplete account information")
	}
	if errContext := ctx.Err(); errContext != nil {
		return errContext
	}
	m.mu.Lock()
	if errContext := ctx.Err(); errContext != nil {
		m.mu.Unlock()
		return errContext
	}
	current := m.auths[base.ID]
	if current == nil || current.RegistrationEpoch != base.RegistrationEpoch || CredentialsChanged(base, current) || accountIdentityChanged(base, current) {
		m.mu.Unlock()
		return fmt.Errorf("account changed during information query")
	}
	updated := current.Clone()
	updated.AccountSnapshot = info.Clone()
	if !info.Quota.ObservedAt.Before(updated.Quota.ObservedAt) {
		updated.Quota.Signals = info.Quota.Clone().Signals
		updated.Quota.ObservedAt = info.Quota.ObservedAt
	}
	if updated.Metadata == nil {
		updated.Metadata = make(map[string]any)
	}
	for key, value := range info.Metadata {
		if !IsAuthTokenPayloadKey(key) && reflect.DeepEqual(current.Metadata[key], base.Metadata[key]) {
			updated.Metadata[key] = value
		}
	}
	if strings.EqualFold(updated.Provider, "codex") && current.Attributes["plan_type"] == base.Attributes["plan_type"] {
		if plan, ok := info.Metadata["plan_type"].(string); ok && strings.TrimSpace(plan) != "" {
			if updated.Attributes == nil {
				updated.Attributes = make(map[string]string)
			}
			updated.Attributes["plan_type"] = strings.TrimSpace(plan)
		}
	}
	updated.Generation++
	updated.UpdatedAt = time.Now()
	m.auths[base.ID] = updated.Clone()
	m.mu.Unlock()
	m.RefreshSchedulerEntry(base.ID)
	_ = m.persist(ctx, updated)
	m.hook.OnAuthUpdated(ctx, updated.Clone())
	managementevents.Publish(managementevents.Accounts)
	return nil
}

func completeAccountInfo(provider string, info *AccountInfo) bool {
	if info == nil || info.Quota.ObservedAt.IsZero() || len(info.Metadata) == 0 {
		return false
	}
	var prefixes []string
	usageSuffix, resetSuffix, scale, relative := "-used-percent", "-reset-at", 100.0, true
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex":
		prefixes = []string{"x-codex-primary", "x-codex-secondary"}
	case "claude":
		prefixes = []string{"anthropic-ratelimit-unified-5h", "anthropic-ratelimit-unified-7d"}
		usageSuffix, resetSuffix, scale, relative = "-utilization", "-reset", 1, false
	default:
		return false
	}
	signals := make(map[string]string, len(info.Quota.Signals))
	for key, value := range info.Quota.Signals {
		signals[strings.ToLower(key)] = strings.TrimSpace(value)
	}
	observations := []quotaResetObservation{{observedAt: info.Quota.ObservedAt, signals: signals}}
	for _, prefix := range prefixes {
		used, errParse := strconv.ParseFloat(signals[prefix+usageSuffix], 64)
		if errParse == nil && !math.IsNaN(used) && !math.IsInf(used, 0) && used >= 0 && used <= scale && !quotaWindowReset(observations, prefix, resetSuffix, relative).IsZero() {
			return true
		}
	}
	return false
}

// accountInfoQuotaBlock excludes known exhausted accounts before their first
// model request. Newer responses can override queried availability, while an
// omitted deadline still falls back to the successful account query.
func accountInfoQuotaBlock(auth *Auth, model string, now time.Time) (bool, blockReason, time.Time) {
	if auth == nil || auth.AccountSnapshot == nil || !freshQuotaResetObservation(auth.AccountSnapshot.Quota, now) {
		return false, blockReasonNone, time.Time{}
	}
	observations := quotaResetObservations(auth, model, now)
	var prefixes []string
	usageSuffix, resetSuffix, scale, relative := "-used-percent", "-reset-at", 100.0, true
	globalRejected := false
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "codex":
		prefixes = []string{"x-codex-primary", "x-codex-secondary"}
		for _, observation := range observations {
			_, primary := observation.signals["x-codex-primary-used-percent"]
			_, secondary := observation.signals["x-codex-secondary-used-percent"]
			allowed, errAllowed := strconv.ParseBool(observation.signals["x-codex-allowed"])
			reached, errReached := strconv.ParseBool(observation.signals["x-codex-limit-reached"])
			if !primary && !secondary && errAllowed != nil && errReached != nil {
				continue
			}
			globalRejected = (errAllowed == nil && !allowed) || (errReached == nil && reached)
			if !globalRejected && errAllowed == nil && allowed {
				return false, blockReasonNone, time.Time{}
			}
			break
		}
	case "claude":
		prefixes = []string{"anthropic-ratelimit-unified-5h", "anthropic-ratelimit-unified-7d"}
		usageSuffix, resetSuffix, scale, relative = "-utilization", "-reset", 1, false
	default:
		return false, blockReasonNone, time.Time{}
	}
	var rejectedResets, otherResets []time.Time
	for _, prefix := range prefixes {
		rawUsed := ""
		for _, observation := range observations {
			if value, exists := observation.signals[prefix+usageSuffix]; exists {
				rawUsed = value
				break
			}
		}
		used, errUsed := strconv.ParseFloat(rawUsed, 64)
		validUsed := errUsed == nil && !math.IsNaN(used) && !math.IsInf(used, 0) && used >= 0 && used <= scale
		rejected := validUsed && used == scale
		if !relative {
			// A newer utilization without status supersedes an older rejection.
			for _, observation := range observations {
				status, hasStatus := observation.signals[prefix+"-status"]
				_, hasUsage := observation.signals[prefix+usageSuffix]
				if !hasStatus && !hasUsage {
					continue
				}
				switch strings.ToLower(status) {
				case "allowed", "allowed_warning":
					rejected = false
				case "rejected":
					rejected = true
				}
				break
			}
		}
		reset := quotaWindowReset(observations, prefix, resetSuffix, relative)
		if rejected {
			rejectedResets = append(rejectedResets, reset)
		} else if validUsed || !reset.IsZero() {
			otherResets = append(otherResets, reset)
		}
	}
	if globalRejected && len(rejectedResets) == 0 {
		rejectedResets = otherResets
		if len(rejectedResets) == 0 {
			return true, blockReasonOther, time.Time{}
		}
	}
	var latest time.Time
	for _, reset := range rejectedResets {
		if reset.IsZero() {
			return true, blockReasonOther, time.Time{}
		}
		if reset.After(now) && reset.After(latest) {
			latest = reset
		}
	}
	if !latest.IsZero() {
		return true, blockReasonCooldown, latest
	}
	return false, blockReasonNone, time.Time{}
}
