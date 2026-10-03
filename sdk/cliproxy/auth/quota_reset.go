package auth

import (
	"math"
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
	quota := auth.Quota
	if state := auth.ModelStates[canonicalModelKey(model)]; state != nil && state.Quota.ObservedAt.After(quota.ObservedAt) {
		quota = state.Quota
	}
	if quota.ObservedAt.IsZero() || quota.ObservedAt.After(now) || now.Sub(quota.ObservedAt) >= quotaResetObservationMaxAge {
		return time.Time{}
	}
	signals := make(map[string]string, len(quota.Signals))
	for key, value := range quota.Signals {
		signals[strings.ToLower(key)] = strings.TrimSpace(value)
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
		used, err := strconv.ParseFloat(signals[prefix+suffix], 64)
		if err != nil || math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > scale {
			continue
		}
		var reset time.Time
		if raw, exists := signals[prefix+resetSuffix]; exists {
			seconds, errParse := strconv.ParseInt(raw, 10, 64)
			if errParse == nil && seconds > 0 {
				reset = time.Unix(seconds, 0)
			}
		} else if strings.EqualFold(auth.Provider, "codex") {
			seconds, errParse := strconv.ParseInt(signals[prefix+"-reset-after-seconds"], 10, 64)
			if errParse == nil && seconds >= 0 && seconds <= int64((time.Duration(1<<63-1))/time.Second) {
				reset = quota.ObservedAt.Add(time.Duration(seconds) * time.Second)
			}
		}
		// A reported exhausted window also blocks preference when its reset is unknown.
		if used == scale && (reset.IsZero() || reset.After(now)) {
			return time.Time{}
		}
		if !reset.After(now) {
			continue
		}
		if next.IsZero() || reset.Before(next) {
			next = reset
		}
	}
	return next
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
