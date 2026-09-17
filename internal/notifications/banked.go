package notifications

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const bankedCountHeader = "x-codex-rate-limit-reset-credits-available-count"

type bankedEpisode struct {
	count    int64
	observed time.Time
}

type bankedObservation struct {
	bankedEpisode
	key, account string
}

// Missing counts are unknown, never zero. The available count is authoritative;
// monetary credit balances and quota percentages do not describe banked resets.
func bankedObservations(accounts []*auth.Auth, now time.Time) []bankedObservation {
	latest := make(map[string]bankedObservation)
	for _, account := range accounts {
		if account == nil || account.Disabled || account.Status == auth.StatusDisabled || !strings.EqualFold(strings.TrimSpace(account.Provider), "codex") {
			continue
		}
		key, label := accountIdentity(account)
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
			for name, value := range quota.Signals {
				if !strings.EqualFold(name, bankedCountHeader) {
					continue
				}
				value = strings.TrimSpace(value)
				if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
					continue
				}
				count, errParse := strconv.ParseInt(value, 10, 64)
				if errParse != nil {
					continue
				}
				previous, exists := latest[key]
				// A tied conflicting snapshot chooses the smaller count rather
				// than inventing a newly earned reset from an ambiguous watermark.
				if !exists || quota.ObservedAt.After(previous.observed) || (quota.ObservedAt.Equal(previous.observed) && count < previous.count) {
					latest[key] = bankedObservation{bankedEpisode{count, quota.ObservedAt}, key, label}
				}
			}
		}
	}
	result := make([]bankedObservation, 0, len(latest))
	for _, item := range latest {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result
}

func (s *Service) pollBanked(ctx context.Context, generation uint64, accounts []*auth.Auth, now time.Time) {
	for _, item := range bankedObservations(accounts, now) {
		s.mu.Lock()
		if generation != s.generation {
			s.mu.Unlock()
			return
		}
		previous, known := s.banked[item.key]
		s.mu.Unlock()
		if known && !item.observed.After(previous.observed) {
			continue
		}
		if item.count > 0 && (!known || item.count > previous.count) {
			title := "Codex banked resets available"
			if known {
				title = "Codex banked reset count increased"
			}
			body := fmt.Sprintf("codex: %s\n%d banked reset(s) available.\nObserved %s.\nRedeem in Codex when needed.", item.account, item.count, item.observed.UTC().Format(time.RFC3339))
			if errSend := s.send(ctx, generation, title, "default", body); errSend != nil {
				return
			}
		}
		s.mu.Lock()
		if generation == s.generation {
			s.banked[item.key] = item.bankedEpisode
		}
		s.mu.Unlock()
	}
}
