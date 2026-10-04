package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCodexAutoExecutorFetchAccountInfoQueriesUsageAndBankedResets(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID: "codex-account", Provider: "codex",
		Metadata: map[string]any{"access_token": "oauth-access", "account_id": "account-one"},
	}
	now := time.Now().UTC()
	early := now.Add(10 * time.Minute).Truncate(time.Second)
	late := now.Add(20 * time.Minute).Truncate(time.Second)
	var paths []string
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", req.Method)
		}
		if _, hasDeadline := req.Context().Deadline(); hasDeadline {
			t.Fatal("account probe imposed a network deadline")
		}
		if req.Header.Get("Authorization") != "Bearer oauth-access" || req.Header.Get("Chatgpt-Account-Id") != "account-one" {
			t.Fatal("account probe did not use the selected OAuth credential")
		}
		paths = append(paths, req.URL.Path)
		var body string
		switch req.URL.Path {
		case "/backend-api/wham/usage":
			body = fmt.Sprintf(`{"plan_type":"plus","account_id":"account-one","user_id":"user-one","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":20,"reset_at":%d,"limit_window_seconds":18000},"secondary_window":{"used_percent":70,"reset_at":%d,"limit_window_seconds":604800}}}`, now.Add(30*time.Minute).Unix(), now.Add(time.Hour).Unix())
		case "/backend-api/wham/rate-limit-reset-credits":
			body = fmt.Sprintf(`{"available_count":3,"credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":%q},{"reset_type":"codex_rate_limits","status":"available","expires_at":%q},{"reset_type":"codex_rate_limits","status":"available","expires_at":null},{"reset_type":"codex_rate_limits","status":"redeemed","expires_at":%q}]}`, late.Format(time.RFC3339), early.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
		default:
			t.Fatalf("unexpected account probe path %q", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	exec := NewCodexAutoExecutor(&config.Config{})
	if !exec.SupportsAccountInfo(auth) {
		t.Fatal("SupportsAccountInfo() = false for OAuth credential")
	}
	info, errFetch := exec.FetchAccountInfo(ctx, auth)
	if errFetch != nil {
		t.Fatal(errFetch)
	}
	if len(paths) != 2 || paths[0] != "/backend-api/wham/usage" || paths[1] != "/backend-api/wham/rate-limit-reset-credits" {
		t.Fatalf("probe paths = %v, want usage followed by credit details", paths)
	}
	if info.Metadata["plan_type"] != "plus" || info.Metadata["account_id"] != "account-one" {
		t.Fatalf("queried account metadata = %#v", info.Metadata)
	}
	if !info.BankedResetAt.Equal(early) {
		t.Fatalf("banked reset = %v, want earliest available expiry %v", info.BankedResetAt, early)
	}
	if info.Quota.ObservedAt.Before(now) || info.Quota.Signals["X-Codex-Primary-Used-Percent"] != "20" || info.Quota.Signals["X-Codex-Rate-Limit-Reset-Credits-Available-Count"] != "3" {
		t.Fatalf("queried quota = %#v", info.Quota)
	}
}

func TestCodexExecutorFetchAccountInfoRequiresCreditDetails(t *testing.T) {
	auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"access_token": "oauth-access"}}
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":10,"reset_at":2000000000,"limit_window_seconds":18000}}}`
		if strings.HasSuffix(req.URL.Path, "rate-limit-reset-credits") {
			status = http.StatusServiceUnavailable
			body = `{"error":"retry later"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	info, errFetch := NewCodexExecutor(&config.Config{}).FetchAccountInfo(ctx, auth)
	if errFetch == nil || info != nil {
		t.Fatalf("FetchAccountInfo() = %#v, %v, want no snapshot on credit query failure", info, errFetch)
	}
}

func TestCodexExecutorSupportsAccountInfoExcludesCompatibleKeys(t *testing.T) {
	exec := NewCodexExecutor(&config.Config{})
	for _, auth := range []*cliproxyauth.Auth{
		nil,
		{Provider: "codex", Attributes: map[string]string{"api_key": "api-key"}},
		{Provider: "codex", Attributes: map[string]string{"base_url": "https://gateway.example/backend-api"}, Metadata: map[string]any{"access_token": "oauth-access"}},
		{Provider: "codex", Attributes: map[string]string{"base_url": "https://chatgpt.com:8443/backend-api"}, Metadata: map[string]any{"access_token": "oauth-access"}},
	} {
		if exec.SupportsAccountInfo(auth) {
			t.Fatalf("SupportsAccountInfo(%#v) = true for non-OAuth or compatible credential", auth)
		}
	}
}

func TestCodexExecutorSupportsAccountInfoKeepsMissingTokenPending(t *testing.T) {
	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"refresh_token": "refresh-only"}}
	if !exec.SupportsAccountInfo(auth) {
		t.Fatal("OAuth credential without access token would bypass account admission")
	}
	info, errFetch := exec.FetchAccountInfo(t.Context(), auth)
	if errFetch == nil || info != nil {
		t.Fatalf("FetchAccountInfo() = %#v, %v, want token acquisition to remain pending", info, errFetch)
	}
}

func TestCodexExecutorFetchAccountInfoRejectsMismatchedAccountIdentity(t *testing.T) {
	auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"access_token": "oauth-access", "account_id": "selected-account"}}
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"plan_type":"plus","account_id":"other-account","rate_limit":{"primary_window":{"used_percent":10,"reset_at":2000000000,"limit_window_seconds":18000}}}`
		if strings.HasSuffix(req.URL.Path, "rate-limit-reset-credits") {
			body = `{"available_count":0,"credits":[]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	info, errFetch := NewCodexExecutor(&config.Config{}).FetchAccountInfo(ctx, auth)
	if errFetch == nil || info != nil {
		t.Fatalf("FetchAccountInfo() = %#v, %v, want no snapshot from another account", info, errFetch)
	}
}
