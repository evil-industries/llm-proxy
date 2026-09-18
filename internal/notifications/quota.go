package notifications

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// ObservationMaxAge bounds how long an idle account's passive quota watermark
// can trigger a new alert. Notifications never probe providers for fresh data.
const ObservationMaxAge = 15 * time.Minute

const recoveryHysteresis = 5.0

type observation struct {
	key       string
	provider  string
	account   string
	window    string
	remaining float64
	observed  time.Time
	reset     time.Time
}

func quotaObservations(accounts []*auth.Auth, now time.Time) []observation {
	latest := make(map[string]observation)
	for _, account := range accounts {
		if account == nil || account.Disabled || account.Status == auth.StatusDisabled {
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(account.Provider))
		if provider != "codex" && provider != "claude" {
			continue
		}
		quotas := []auth.QuotaState{account.Quota}
		for _, model := range account.ModelStates {
			if model != nil {
				quotas = append(quotas, model.Quota)
			}
		}
		for _, quota := range quotas {
			if quota.ObservedAt.IsZero() || quota.ObservedAt.After(now) || now.Sub(quota.ObservedAt) > ObservationMaxAge {
				continue
			}
			signals := make(map[string]string, len(quota.Signals))
			for name, value := range quota.Signals {
				signals[strings.ToLower(name)] = strings.TrimSpace(value)
			}
			identity, label := accountIdentity(account)
			for name, value := range signals {
				var prefix, window string
				var scale float64
				switch {
				case provider == "codex" && strings.HasPrefix(name, "x-codex-") && strings.HasSuffix(name, "-used-percent"):
					prefix = strings.TrimSuffix(name, "-used-percent")
					window = strings.TrimPrefix(prefix, "x-codex-")
					scale = 100
				case provider == "claude" && strings.HasPrefix(name, "anthropic-ratelimit-unified-") && strings.HasSuffix(name, "-utilization"):
					prefix = strings.TrimSuffix(name, "-utilization")
					window = strings.TrimPrefix(prefix, "anthropic-ratelimit-unified-")
					scale = 1
				default:
					continue
				}
				used, errParse := strconv.ParseFloat(value, 64)
				if errParse != nil || math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > scale || window == "" {
					continue
				}
				reset, valid := quotaReset(signals, prefix, provider, quota.ObservedAt)
				if !valid || (!reset.IsZero() && !reset.After(now)) {
					continue
				}
				windowKey := window
				if provider == "codex" {
					windowKey, window = codexQuotaWindow(signals, window)
				}
				item := observation{
					key: identity + "\x00" + windowKey, provider: provider, account: label,
					window: window, remaining: 100 * (1 - used/scale), observed: quota.ObservedAt, reset: reset,
				}
				previous, exists := latest[item.key]
				if !exists || item.observed.After(previous.observed) || (item.observed.Equal(previous.observed) && item.remaining < previous.remaining) {
					latest[item.key] = item
				}
			}
		}
	}
	result := make([]observation, 0, len(latest))
	for _, item := range latest {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result
}

// codexQuotaWindow separates metered pools from their primary/secondary window.
// Explicit limit names join HTTP namespaces and websocket additional limits;
// unknown aliases remain separate rather than guessing from a model name.
func codexQuotaWindow(signals map[string]string, window string) (string, string) {
	var pool, period string
	if window == "primary" || window == "secondary" {
		pool = strings.ToLower(strings.TrimSpace(signals["x-codex-active-limit"]))
		if pool == "" {
			return window, window
		}
		pool = strings.TrimPrefix(pool, "codex_")
		period = window
	} else {
		for _, suffix := range []string{"-primary", "-secondary"} {
			if strings.HasSuffix(window, suffix) {
				pool = strings.TrimSuffix(window, suffix)
				period = strings.TrimPrefix(suffix, "-")
				break
			}
		}
		if pool == "" {
			return window, window
		}
	}
	if name := strings.TrimSpace(signals["x-codex-"+pool+"-limit-name"]); name != "" {
		return "name\x00" + strings.ToLower(name) + "\x00" + period, name + " " + period
	}
	return "pool\x00" + pool + "\x00" + period, pool + "-" + period
}

func quotaReset(signals map[string]string, prefix, provider string, observed time.Time) (time.Time, bool) {
	name := prefix + "-reset-at"
	if provider == "claude" {
		name = prefix + "-reset"
	}
	if value, exists := signals[name]; exists {
		seconds, errParse := strconv.ParseInt(value, 10, 64)
		if errParse != nil || seconds <= 0 {
			return time.Time{}, false
		}
		return time.Unix(seconds, 0), true
	}
	if provider == "codex" {
		if value, exists := signals[prefix+"-reset-after-seconds"]; exists {
			seconds, errParse := strconv.ParseInt(value, 10, 64)
			if errParse != nil || seconds < 0 || seconds > math.MaxInt64/int64(time.Second) {
				return time.Time{}, false
			}
			return observed.Add(time.Duration(seconds) * time.Second), true
		}
	}
	return time.Time{}, true
}

func accountIdentity(account *auth.Auth) (string, string) {
	identity := ""
	for _, name := range []string{"account_id", "chatgpt_account_id", "email"} {
		if value := strings.TrimSpace(account.Attributes[name]); value != "" {
			identity = value
			break
		}
		if value, ok := account.Metadata[name].(string); ok && strings.TrimSpace(value) != "" {
			identity = strings.TrimSpace(value)
			break
		}
	}
	if identity == "" {
		identity = account.ID
	}
	fingerprint := sha256.Sum256([]byte(strings.ToLower(account.Provider) + "\x00" + identity))
	key := hex.EncodeToString(fingerprint[:])
	label := strings.TrimSpace(account.Label)
	if label == "" {
		label = "account " + key[:12]
	}
	// Labels are notification body text, never headers or credential metadata.
	label = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, label)
	return key, label
}
