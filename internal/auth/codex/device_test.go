package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type deviceRoundTrip func(*http.Request) (*http.Response, error)

func (f deviceRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func deviceResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func deviceJWT() string {
	return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"test@example.com","https://api.openai.com/auth":{"chatgpt_account_id":"account-1","chatgpt_plan_type":"plus"}}`)) + ".signature"
}

func TestDeviceAuthProtocolPendingSlowdownAndExchange(t *testing.T) {
	now := time.Now()
	d := NewDeviceAuth(nil)
	d.now = func() time.Time { return now }
	var waits []time.Duration
	d.wait = func(ctx context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		now = now.Add(duration)
		return ctx.Err()
	}
	polls := 0
	d.auth.httpClient.Transport = deviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "auth.openai.com" || r.Method != http.MethodPost {
			t.Fatalf("unexpected endpoint %s", r.URL)
		}
		switch r.URL.String() {
		case deviceCodeURL:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != ClientID {
				t.Fatal("missing client ID")
			}
			return deviceResponse(200, `{"device_auth_id":"private-device-id","usercode":"ABCD-EFGH","interval":"2","expires_in":900}`), nil
		case deviceTokenURL:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["device_auth_id"] != "private-device-id" || body["user_code"] != "ABCD-EFGH" {
				t.Fatal("wrong polling challenge")
			}
			polls++
			if polls == 1 {
				return deviceResponse(403, `{"error":"authorization_pending"}`), nil
			}
			if polls == 2 {
				return deviceResponse(429, `{"error":"slow_down"}`), nil
			}
			return deviceResponse(200, `{"authorization_code":"private-auth-code","code_verifier":"private-verifier","code_challenge":"private-challenge"}`), nil
		case TokenURL:
			_ = r.ParseForm()
			if r.Form.Get("redirect_uri") != deviceRedirectURI || r.Form.Get("code_verifier") != "private-verifier" || r.Form.Get("code") != "private-auth-code" {
				t.Fatal("wrong token exchange")
			}
			b, _ := json.Marshal(map[string]any{"access_token": "private-access", "refresh_token": "private-refresh", "id_token": deviceJWT(), "expires_in": 3600})
			return deviceResponse(200, string(b)), nil
		default:
			t.Fatalf("unexpected endpoint %s", r.URL)
			return nil, nil
		}
	})
	challenge, err := d.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(challenge)
	if strings.Contains(string(public), "private") || challenge.VerificationURI != DeviceVerificationURI {
		t.Fatalf("unsafe challenge %s", public)
	}
	var changed time.Duration
	bundle, err := d.Complete(context.Background(), challenge, func(v time.Duration) { changed = v })
	if err != nil {
		t.Fatal(err)
	}
	if bundle.TokenData.Email != "test@example.com" || bundle.TokenData.AccessToken != "private-access" {
		t.Fatalf("bad exchange %+v", bundle)
	}
	if !reflect.DeepEqual(waits, []time.Duration{2 * time.Second, 2 * time.Second, 7 * time.Second}) || changed != 7*time.Second {
		t.Fatalf("poll intervals: %v, changed %v", waits, changed)
	}
}

func TestDeviceAuthFailuresAndExpiry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"denied", 400, `{"error":"access_denied"}`, ErrDeviceDenied},
		{"expired", 400, `{"error":"expired_token"}`, ErrDeviceExpired},
		{"server error", 500, `private-token-secret`, ErrDeviceUnavailable},
		{"malformed success", 200, `{"authorization_code":"secret"}`, ErrDeviceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDeviceAuth(nil)
			d.wait = func(context.Context, time.Duration) error { return nil }
			d.auth.httpClient.Transport = deviceRoundTrip(func(*http.Request) (*http.Response, error) { return deviceResponse(tc.status, tc.body), nil })
			_, err := d.Complete(context.Background(), &DeviceChallenge{deviceAuthID: "private", UserCode: "code", Interval: time.Second, ExpiresAt: time.Now().Add(time.Minute)}, nil)
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
	t.Run("expiry bounds next wait", func(t *testing.T) {
		d := NewDeviceAuth(nil)
		now := time.Now()
		d.now = func() time.Time { return now }
		d.wait = func(_ context.Context, duration time.Duration) error {
			if duration != time.Second {
				t.Fatalf("wait passed expiry: %s", duration)
			}
			now = now.Add(duration)
			return nil
		}
		d.auth.httpClient.Transport = deviceRoundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("polled after expiry"); return nil, nil })
		_, err := d.Complete(context.Background(), &DeviceChallenge{deviceAuthID: "private", Interval: 5 * time.Second, ExpiresAt: now.Add(time.Second)}, nil)
		if !errors.Is(err, ErrDeviceExpired) {
			t.Fatal(err)
		}
	})
	t.Run("cancel during poll", func(t *testing.T) {
		d := NewDeviceAuth(nil)
		ctx, cancel := context.WithCancel(context.Background())
		d.wait = func(context.Context, time.Duration) error { return nil }
		d.auth.httpClient.Transport = deviceRoundTrip(func(r *http.Request) (*http.Response, error) {
			cancel()
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		_, err := d.Complete(ctx, &DeviceChallenge{deviceAuthID: "private", Interval: time.Second, ExpiresAt: time.Now().Add(time.Minute)}, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestDeviceAuthStartValidationAndPollInterval(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"device_auth_id":"private"}`, `{"user_code":"code"}`, `invalid`} {
		d := NewDeviceAuth(nil)
		d.auth.httpClient.Transport = deviceRoundTrip(func(*http.Request) (*http.Response, error) { return deviceResponse(200, body), nil })
		if _, err := d.Start(context.Background()); !errors.Is(err, ErrDeviceUnavailable) {
			t.Fatalf("accepted %s: %v", body, err)
		}
	}
	for _, raw := range []string{`null`, `false`, `-1`, `0`, `0.5`, `9223372036854775807`, `"garbage"`} {
		if got := deviceSeconds(json.RawMessage(raw), 5*time.Second); got != 5*time.Second {
			t.Fatalf("bad interval %s: %s", raw, got)
		}
	}
	d := NewDeviceAuth(nil)
	if err := d.auth.httpClient.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal("device credentials must not follow redirects")
	}
}
