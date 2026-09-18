package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const (
	DeviceVerificationURI = "https://auth.openai.com/codex/device"
	DeviceAuthLifetime    = 15 * time.Minute
	deviceCodeURL         = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	deviceTokenURL        = "https://auth.openai.com/api/accounts/deviceauth/token"
	deviceRedirectURI     = "https://auth.openai.com/deviceauth/callback"
)

var (
	ErrDeviceExpired     = errors.New("device authentication expired")
	ErrDeviceDenied      = errors.New("device authentication was denied")
	ErrDeviceUnavailable = errors.New("device authentication is unavailable")
)

// DeviceChallenge separates the user-facing challenge from the private polling ID.
// Pass the returned value unchanged to Complete; never serialize provider IDs.
type DeviceChallenge struct {
	VerificationURI string        `json:"verification_uri"`
	UserCode        string        `json:"user_code"`
	ExpiresAt       time.Time     `json:"expires_at"`
	Interval        time.Duration `json:"-"`
	deviceAuthID    string
}

// DeviceAuth implements the same official device protocol for CLI and management.
// A service belongs to one flow, retaining the proxy configuration from its start.
type DeviceAuth struct {
	auth *CodexAuth
	now  func() time.Time
	wait func(context.Context, time.Duration) error
}

func NewDeviceAuth(cfg *config.Config) *DeviceAuth {
	auth := NewCodexAuth(cfg)
	client := *auth.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	auth.httpClient = &client
	return &DeviceAuth{auth: auth, now: time.Now, wait: waitDevicePoll}
}

func waitDevicePoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func deviceSeconds(raw json.RawMessage, fallback time.Duration) time.Duration {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "\"") {
		if err := json.Unmarshal(raw, &text); err != nil {
			return fallback
		}
		text = strings.TrimSpace(text)
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/int64(time.Second) {
		return fallback
	}
	return time.Duration(n) * time.Second
}

func (d *DeviceAuth) post(ctx context.Context, endpoint string, payload any) ([]byte, int, http.Header, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, nil, ErrDeviceUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, 0, nil, ErrDeviceUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := d.auth.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, nil, ctx.Err()
		}
		return nil, 0, nil, ErrDeviceUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	data, err = io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, nil, ctx.Err()
		}
		return nil, 0, nil, ErrDeviceUnavailable
	}
	return data, resp.StatusCode, resp.Header, nil
}

func (d *DeviceAuth) Start(ctx context.Context) (*DeviceChallenge, error) {
	started := d.now()
	data, status, _, err := d.post(ctx, deviceCodeURL, map[string]string{"client_id": ClientID})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, ErrDeviceUnavailable
	}
	var response struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		UserCodeAlt  string          `json:"usercode"`
		Interval     json.RawMessage `json:"interval"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
	}
	if json.Unmarshal(data, &response) != nil {
		return nil, ErrDeviceUnavailable
	}
	code := strings.TrimSpace(response.UserCode)
	if code == "" {
		code = strings.TrimSpace(response.UserCodeAlt)
	}
	if code == "" || strings.TrimSpace(response.DeviceAuthID) == "" {
		return nil, ErrDeviceUnavailable
	}
	return &DeviceChallenge{
		VerificationURI: DeviceVerificationURI, UserCode: code, deviceAuthID: response.DeviceAuthID,
		Interval:  deviceSeconds(response.Interval, 5*time.Second),
		ExpiresAt: started.Add(deviceSeconds(response.ExpiresIn, DeviceAuthLifetime)),
	}, nil
}

// Complete polls independently of browser requests and exchanges the resulting
// authorization code. onInterval reports slow-down changes without exposing secrets.
func (d *DeviceAuth) Complete(ctx context.Context, challenge *DeviceChallenge, onInterval func(time.Duration)) (*CodexAuthBundle, error) {
	if challenge == nil || challenge.deviceAuthID == "" {
		return nil, ErrDeviceUnavailable
	}
	remaining := challenge.ExpiresAt.Sub(d.now())
	if remaining <= 0 {
		return nil, ErrDeviceExpired
	}
	// Timeouts apply only to this credential acquisition, including a stalled endpoint.
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	interval := challenge.Interval
	for {
		if !d.now().Before(challenge.ExpiresAt) {
			return nil, ErrDeviceExpired
		}
		wait := interval
		if left := challenge.ExpiresAt.Sub(d.now()); wait > left {
			wait = left
		}
		if err := d.wait(ctx, wait); err != nil {
			return nil, deviceContextError(err)
		}
		if !d.now().Before(challenge.ExpiresAt) {
			return nil, ErrDeviceExpired
		}
		data, status, headers, err := d.post(ctx, deviceTokenURL, map[string]string{"device_auth_id": challenge.deviceAuthID, "user_code": challenge.UserCode})
		if err != nil {
			return nil, deviceContextError(err)
		}
		if status >= 200 && status < 300 {
			var response struct {
				AuthorizationCode string `json:"authorization_code"`
				CodeVerifier      string `json:"code_verifier"`
				CodeChallenge     string `json:"code_challenge"`
			}
			if json.Unmarshal(data, &response) != nil || strings.TrimSpace(response.AuthorizationCode) == "" || strings.TrimSpace(response.CodeVerifier) == "" || strings.TrimSpace(response.CodeChallenge) == "" {
				return nil, ErrDeviceUnavailable
			}
			bundle, errExchange := d.auth.ExchangeCodeForTokensWithRedirect(ctx, response.AuthorizationCode, deviceRedirectURI, &PKCECodes{CodeVerifier: response.CodeVerifier, CodeChallenge: response.CodeChallenge})
			if errExchange != nil {
				if ctx.Err() != nil {
					return nil, deviceContextError(ctx.Err())
				}
				return nil, fmt.Errorf("device token exchange failed")
			}
			if bundle == nil || bundle.TokenData.AccessToken == "" || bundle.TokenData.RefreshToken == "" || bundle.TokenData.Email == "" {
				return nil, ErrDeviceUnavailable
			}
			return bundle, nil
		}
		var failure struct {
			Error json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		var code string
		if json.Unmarshal(failure.Error, &code) != nil {
			var detail struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(failure.Error, &detail)
			code = detail.Code
		}
		switch code {
		case "expired_token", "expired":
			return nil, ErrDeviceExpired
		case "access_denied", "authorization_declined":
			return nil, ErrDeviceDenied
		}
		switch {
		case code == "slow_down" || status == http.StatusTooManyRequests:
			if interval <= time.Duration(math.MaxInt64)-5*time.Second {
				interval += 5 * time.Second
			}
			retry := deviceSeconds(json.RawMessage(strconv.Quote(headers.Get("Retry-After"))), 0)
			if date, errDate := http.ParseTime(headers.Get("Retry-After")); errDate == nil && date.Sub(d.now()) > retry {
				retry = date.Sub(d.now())
			}
			if retry > interval {
				interval = retry
			}
			if onInterval != nil {
				onInterval(interval)
			}
		case code == "authorization_pending" || status == http.StatusForbidden || status == http.StatusNotFound:
		default:
			return nil, ErrDeviceUnavailable
		}
	}
}

func deviceContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrDeviceExpired
	}
	return err
}
