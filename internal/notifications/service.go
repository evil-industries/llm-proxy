// Package notifications sends passive quota alerts independently of inference.
package notifications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const pollInterval = 30 * time.Second

var (
	ErrNotConfigured = errors.New("notification destination is not configured")
	ErrBusy          = errors.New("a notification is already being sent")
)

// Status contains delivery health only, never destination credentials.
type Status struct {
	Enabled       bool       `json:"enabled"`
	Configured    bool       `json:"configured"`
	InFlight      bool       `json:"in_flight"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	NextRetryAt   *time.Time `json:"next_retry_at,omitempty"`
}

type episode struct {
	level     int
	reset     time.Time
	seen      time.Time
	observed  time.Time
	remaining float64
}

// Service is safe to configure and inspect while its independent worker runs.
type Service struct {
	mu         sync.Mutex
	source     func() []*auth.Auth
	client     *http.Client
	now        func() time.Time
	cfg        config.NotificationsConfig
	generation uint64
	configured bool
	status     Status
	episodes   map[string]episode
	banked     map[string]bankedEpisode
	failures   uint
	cancelSend context.CancelFunc
	running    bool
}

func New(source func() []*auth.Auth) *Service {
	return &Service{
		source: source, now: time.Now, episodes: make(map[string]episode), banked: make(map[string]bankedEpisode),
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// Configure replaces settings and cancels delivery to an obsolete destination.
// Invalid settings fail closed, including when supplied outside the management API.
func (s *Service) Configure(cfg config.NotificationsConfig) {
	if s == nil {
		return
	}
	if cfg == (config.NotificationsConfig{}) {
		cfg = config.DefaultNotificationsConfig()
	}
	if cfg.WarningPercent == 0 && cfg.CriticalPercent == 0 {
		cfg.WarningPercent, cfg.CriticalPercent = 20, 5
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation > 0 && s.cfg == cfg {
		return
	}
	if s.cancelSend != nil {
		s.cancelSend()
	}
	s.cfg = cfg
	s.generation++
	s.episodes = make(map[string]episode)
	s.banked = make(map[string]bankedEpisode)
	s.failures = 0
	s.status = Status{Enabled: cfg.Enabled, InFlight: s.status.InFlight}
	validation := cfg
	validation.Enabled = true
	s.configured = validation.Validate() == nil
	s.status.Configured = s.configured
	if cfg.Enabled && !s.configured {
		s.status.LastError = ErrNotConfigured.Error()
	}
}

func (s *Service) Status() Status {
	if s == nil {
		return Status{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	if status.LastSuccessAt != nil {
		copyTime := *status.LastSuccessAt
		status.LastSuccessAt = &copyTime
	}
	if status.NextRetryAt != nil {
		copyTime := *status.NextRetryAt
		status.NextRetryAt = &copyTime
	}
	return status
}

// Run polls passive observations immediately and every thirty seconds until canceled.
// Network I/O happens only on this worker, never on an inference request.
func (s *Service) Run(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) poll(ctx context.Context) {
	s.mu.Lock()
	cfg, generation := s.cfg, s.generation
	now := s.now()
	ready := cfg.Enabled && s.configured && !s.status.InFlight && (s.status.NextRetryAt == nil || !now.Before(*s.status.NextRetryAt))
	s.mu.Unlock()
	if !ready || s.source == nil {
		return
	}
	accounts := s.source()
	for _, item := range quotaObservations(accounts, now) {
		s.mu.Lock()
		if generation != s.generation {
			s.mu.Unlock()
			return
		}
		previous := s.episodes[item.key]
		s.mu.Unlock()
		if item.observed.Before(previous.observed) {
			continue
		}
		newer := !previous.observed.IsZero() && item.observed.After(previous.observed)
		// A timer alone is not confirmation: require a newer observation in a
		// later quota window. Recovery can also happen before a scheduled reset.
		reset := newer && !previous.reset.IsZero() && !item.observed.Before(previous.reset) && item.reset.After(previous.reset)
		recovered := newer && (previous.level > 0 || previous.remaining <= cfg.WarningPercent) && item.remaining > cfg.WarningPercent+recoveryHysteresis
		if cfg.ResetNotifications && (reset || recovered) {
			title := "Quota recovered"
			if reset {
				title = "Quota window reset observed"
			}
			if errSend := s.send(ctx, generation, title, "default", quotaMessage(item)); errSend != nil {
				return // Keep the previous observation so failed reset alerts can retry.
			}
		}
		if reset || item.remaining > cfg.WarningPercent+recoveryHysteresis {
			previous.level = 0
		}
		previous.seen = now
		previous.observed = item.observed
		previous.remaining = item.remaining
		if !item.reset.IsZero() {
			previous.reset = item.reset
		}
		s.mu.Lock()
		if generation != s.generation {
			s.mu.Unlock()
			return
		}
		s.episodes[item.key] = previous
		s.mu.Unlock()
		level := 0
		if item.remaining <= cfg.CriticalPercent+1e-9 {
			level = 2
		} else if item.remaining <= cfg.WarningPercent+1e-9 {
			level = 1
		}
		if level == 0 || level <= previous.level {
			continue
		}
		title, priority := "Low remaining quota", "high"
		if level == 2 {
			title, priority = "Critically low remaining quota", "urgent"
		}
		if errSend := s.send(ctx, generation, title, priority, quotaMessage(item)); errSend != nil {
			return
		}
		s.mu.Lock()
		if generation == s.generation {
			previous.level = level
			s.episodes[item.key] = previous
		}
		s.mu.Unlock()
	}
	if cfg.BankedResetNotifications {
		s.pollBanked(ctx, generation, accounts, now)
	}
	// Retain dormant episodes through week-long quota windows without keeping
	// entries for credentials removed indefinitely.
	s.mu.Lock()
	for key, previous := range s.episodes {
		if now.Sub(previous.seen) > 30*24*time.Hour {
			delete(s.episodes, key)
		}
	}
	for key, previous := range s.banked {
		if now.Sub(previous.observed) > 30*24*time.Hour {
			delete(s.banked, key)
		}
	}
	s.mu.Unlock()
}

func quotaMessage(item observation) string {
	body := fmt.Sprintf("%s: %s\n%s: %.1f%% remaining.\nObserved %s.", item.provider, item.account, item.window, item.remaining, item.observed.UTC().Format(time.RFC3339))
	if !item.reset.IsZero() {
		body += "\nResets " + item.reset.UTC().Format(time.RFC3339) + "."
	}
	return body
}

// SendTest is only invoked by an explicit operator action. A configured destination
// may be tested before automatic alerts are enabled.
func (s *Service) SendTest(ctx context.Context) error {
	if s == nil {
		return ErrNotConfigured
	}
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	return s.send(ctx, generation, "Test notification", "default", "Notification delivery is working. Quota alerts use recent provider observations.")
}

func (s *Service) send(ctx context.Context, generation uint64, title, priority, body string) error {
	s.mu.Lock()
	if generation != s.generation || !s.configured {
		s.mu.Unlock()
		return ErrNotConfigured
	}
	if s.status.InFlight {
		s.mu.Unlock()
		return ErrBusy
	}
	cfg := s.cfg
	sendCtx, cancel := context.WithCancel(ctx)
	s.cancelSend = cancel
	s.status.InFlight = true
	s.mu.Unlock()

	errSend := s.publish(sendCtx, cfg, title, priority, body)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.InFlight = false
	s.cancelSend = nil
	if generation != s.generation {
		return errors.New("notification settings changed during delivery")
	}
	now := s.now()
	if errSend != nil {
		s.status.LastError = errSend.Error()
		if s.failures < 5 {
			s.failures++
		}
		delay := pollInterval * time.Duration(1<<(s.failures-1))
		retryAt := now.Add(delay)
		s.status.NextRetryAt = &retryAt
		return errSend
	}
	s.failures = 0
	s.status.LastError = ""
	s.status.NextRetryAt = nil
	s.status.LastSuccessAt = &now
	return nil
}

func (s *Service) publish(ctx context.Context, cfg config.NotificationsConfig, title, priority, body string) error {
	endpoint := strings.TrimRight(cfg.URL, "/") + "/" + cfg.Topic
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if errRequest != nil {
		return errors.New("notification request could not be created")
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	response, errSend := s.client.Do(req)
	if errSend != nil {
		if ctx.Err() != nil {
			return errors.New("notification delivery was canceled")
		}
		return errors.New("notification delivery failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("notification server returned HTTP %d", response.StatusCode)
	}
	return nil
}
