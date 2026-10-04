package auth

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const quotaResetObservationMaxAge = 15 * time.Minute

// nextQuotaReset uses only fresh, usable provider windows. Cooldown deadlines
// are not allowance resets, and expired observations never imply a refill.
func nextQuotaReset(auth *Auth, model string, now time.Time) time.Time {
	if auth == nil {
		return time.Time{}
	}
	observations := quotaResetObservations(auth, model, now)
	if len(observations) == 0 {
		return time.Time{}
	}
	queriedUsable := false
	if auth.AccountSnapshot != nil && freshQuotaResetObservation(auth.AccountSnapshot.Quota, now) {
		blocked, _, _ := accountInfoQuotaBlock(auth, model, now)
		queriedUsable = !blocked
	}
	var next time.Time
	var prefixes []string
	switch strings.ToLower(auth.Provider) {
	case "codex":
		prefixes = []string{"x-codex-primary", "x-codex-secondary"}
	case "claude":
		prefixes = []string{"anthropic-ratelimit-unified-5h", "anthropic-ratelimit-unified-7d"}
	default:
		return next
	}
	for _, prefix := range prefixes {
		suffix, scale, resetSuffix := "-used-percent", 100.0, "-reset-at"
		if strings.EqualFold(auth.Provider, "claude") {
			suffix, scale, resetSuffix = "-utilization", 1, "-reset"
		}
		rawUsed, _ := latestQuotaResetSignal(observations, prefix+suffix)
		used, err := strconv.ParseFloat(rawUsed, 64)
		if err != nil || math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > scale {
			continue
		}
		reset := quotaWindowReset(observations, prefix, resetSuffix, strings.EqualFold(auth.Provider, "codex"))
		// A reported exhausted window also blocks preference when its reset is unknown.
		if used == scale && !queriedUsable && (reset.IsZero() || reset.After(now)) {
			return time.Time{}
		}
		if !reset.After(now) {
			continue
		}
		if next.IsZero() || reset.Before(next) {
			next = reset
		}
	}
	if strings.EqualFold(auth.Provider, "codex") && auth.AccountSnapshot != nil &&
		freshQuotaResetObservation(auth.AccountSnapshot.Quota, now) && auth.AccountSnapshot.BankedResetAt.After(now) {
		// Banked resets are optional expiring resources. Their deadline only
		// affects routing preference; routing never redeems a reset credit.
		rawCount, knownCount := latestQuotaResetSignal(observations, strings.ToLower(codexResetCreditsCountHeader))
		count, validCount := normalizedResetCreditCount(rawCount)
		blocked, _, _ := isAuthBlockedForModel(auth, model, now)
		if knownCount && validCount && count != "0" && !blocked &&
			(next.IsZero() || auth.AccountSnapshot.BankedResetAt.Before(next)) {
			next = auth.AccountSnapshot.BankedResetAt
		}
	}
	return next
}

type quotaResetObservation struct {
	observedAt time.Time
	signals    map[string]string
}

func freshQuotaResetObservation(quota QuotaState, now time.Time) bool {
	return !quota.ObservedAt.IsZero() && !quota.ObservedAt.After(now) && now.Sub(quota.ObservedAt) < quotaResetObservationMaxAge
}

// Keep queried deadlines when a newer passive snapshot omits them, while an
// explicitly reported value from the newer snapshot always takes precedence.
func quotaResetObservations(auth *Auth, model string, now time.Time) []quotaResetObservation {
	quotas := []QuotaState{auth.Quota}
	if state := auth.ModelStates[canonicalModelKey(model)]; state != nil {
		quotas = append(quotas, state.Quota)
	}
	if auth.AccountSnapshot != nil {
		quotas = append(quotas, auth.AccountSnapshot.Quota)
	}
	observations := make([]quotaResetObservation, 0, len(quotas))
	for _, quota := range quotas {
		if !freshQuotaResetObservation(quota, now) {
			continue
		}
		signals := make(map[string]string, len(quota.Signals))
		for key, value := range quota.Signals {
			signals[strings.ToLower(key)] = strings.TrimSpace(value)
		}
		observations = append(observations, quotaResetObservation{observedAt: quota.ObservedAt, signals: signals})
	}
	sort.SliceStable(observations, func(i, j int) bool {
		return observations[i].observedAt.After(observations[j].observedAt)
	})
	return observations
}

func latestQuotaResetSignal(observations []quotaResetObservation, name string) (string, bool) {
	for _, observation := range observations {
		if value, exists := observation.signals[name]; exists {
			return value, true
		}
	}
	return "", false
}

func quotaWindowReset(observations []quotaResetObservation, prefix, resetSuffix string, allowRelative bool) time.Time {
	for _, observation := range observations {
		if raw, exists := observation.signals[prefix+resetSuffix]; exists {
			seconds, errParse := strconv.ParseInt(raw, 10, 64)
			if errParse == nil && seconds > 0 {
				return time.Unix(seconds, 0)
			}
			return time.Time{}
		}
		if allowRelative {
			if raw, exists := observation.signals[prefix+"-reset-after-seconds"]; exists {
				seconds, errParse := strconv.ParseInt(raw, 10, 64)
				if errParse == nil && seconds >= 0 && seconds <= int64((time.Duration(1<<63-1))/time.Second) {
					return observation.observedAt.Add(time.Duration(seconds) * time.Second)
				}
				return time.Time{}
			}
		}
	}
	return time.Time{}
}

// preferSoonestQuotaReset preserves order for tied deadlines so the configured
// selector remains the tie breaker. Unknown resets retain the existing fallback.
func preferSoonestQuotaReset(auths []*Auth, now time.Time, modelForAuth func(*Auth) string) []*Auth {
	var earliest time.Time
	var preferred []*Auth
	for _, candidate := range highestPriorityAuths(auths) {
		reset := nextQuotaReset(candidate, modelForAuth(candidate), now)
		if reset.IsZero() {
			continue
		}
		if earliest.IsZero() || reset.Before(earliest) {
			earliest = reset
			preferred = nil
		}
		if reset.Equal(earliest) {
			preferred = append(preferred, candidate)
		}
	}
	if len(preferred) > 0 {
		return preferred
	}
	return auths
}

func hasModelQuotaSignals(auth *Auth) bool {
	for _, state := range auth.ModelStates {
		if state != nil && len(state.Quota.Signals) > 0 {
			return true
		}
	}
	return false
}
