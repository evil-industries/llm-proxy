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

func TestClaudeExecutorFetchAccountInfoQueriesUsageAndRealProfile(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID: "claude-account", Provider: "claude",
		Attributes: map[string]string{"api_key": "sk-ant-oat-account"},
		Metadata:   map[string]any{"account_uuid": "synthetic-cached-identity"},
	}
	now := time.Now().UTC()
	fiveHourReset := now.Add(time.Hour).Truncate(time.Second)
	sevenDayReset := now.Add(24 * time.Hour).Truncate(time.Second)
	var paths []string
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "Bearer sk-ant-oat-account" || req.Header.Get("X-Api-Key") != "" {
			t.Fatal("account probe did not use the selected OAuth Bearer token")
		}
		if _, hasDeadline := req.Context().Deadline(); hasDeadline {
			t.Fatal("account probe imposed a network deadline")
		}
		paths = append(paths, req.URL.Path)
		var body string
		switch req.URL.Path {
		case "/api/oauth/usage":
			body = fmt.Sprintf(`{"five_hour":{"utilization":25,"resets_at":%q},"seven_day":{"utilization":75,"resets_at":%q}}`, fiveHourReset.Format(time.RFC3339), sevenDayReset.Format(time.RFC3339))
		case "/api/oauth/profile":
			body = `{"account":{"uuid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","email":"user@example.com"},"organization":{"uuid":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Example"}}`
		default:
			t.Fatalf("unexpected path %q", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	exec := NewClaudeExecutor(&config.Config{})
	if !exec.SupportsAccountInfo(auth) {
		t.Fatal("SupportsAccountInfo() = false for configured OAuth token")
	}
	info, errFetch := exec.FetchAccountInfo(ctx, auth)
	if errFetch != nil {
		t.Fatal(errFetch)
	}
	if len(paths) != 2 || paths[0] != "/api/oauth/usage" || paths[1] != "/api/oauth/profile" {
		t.Fatalf("probe paths = %v, want usage followed by profile", paths)
	}
	if info.Metadata["account_uuid"] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || info.Metadata["email"] != "user@example.com" {
		t.Fatalf("queried account metadata = %#v", info.Metadata)
	}
	if info.Quota.ObservedAt.Before(now) || info.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Utilization"] != "0.25" || info.Quota.Signals["Anthropic-Ratelimit-Unified-7d-Utilization"] != "0.75" {
		t.Fatalf("queried quota = %#v", info.Quota)
	}
	if auth.Attributes["api_key"] != "sk-ant-oat-account" || auth.Metadata["account_uuid"] != "synthetic-cached-identity" {
		t.Fatal("account probe mutated the input credential")
	}
}

func TestClaudeExecutorFetchAccountInfoRejectsUnavailableProfile(t *testing.T) {
	for _, profile := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":"scope"}`},
		{name: "empty account", status: http.StatusOK, body: `{"account":{}}`},
		{name: "malformed UUID", status: http.StatusOK, body: `{"account":{"uuid":"fallback"}}`},
	} {
		t.Run(profile.name, func(t *testing.T) {
			auth := &cliproxyauth.Auth{Provider: "claude", Metadata: map[string]any{"access_token": "sk-ant-oat-account", "account_uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				status := http.StatusOK
				body := `{"five_hour":{"utilization":20,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":{"utilization":60,"resets_at":"2030-01-02T00:00:00Z"}}`
				if strings.HasSuffix(req.URL.Path, "profile") {
					status, body = profile.status, profile.body
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			info, errFetch := NewClaudeExecutor(&config.Config{}).FetchAccountInfo(ctx, auth)
			if errFetch == nil || info != nil {
				t.Fatalf("FetchAccountInfo() = %#v, %v, want no snapshot without real account metadata", info, errFetch)
			}
		})
	}
}

func TestClaudeExecutorSupportsAccountInfoExcludesCompatibleKeys(t *testing.T) {
	exec := NewClaudeExecutor(&config.Config{})
	for _, auth := range []*cliproxyauth.Auth{
		nil,
		{Provider: "claude", Attributes: map[string]string{"api_key": "sk-ant-api-account"}},
		{Provider: "claude", Attributes: map[string]string{"base_url": "https://gateway.example", "api_key": "sk-ant-oat-account"}},
		{Provider: "claude", Attributes: map[string]string{"base_url": "https://api.anthropic.com:8443", "api_key": "sk-ant-oat-account"}},
	} {
		if exec.SupportsAccountInfo(auth) {
			t.Fatalf("SupportsAccountInfo(%#v) = true for non-OAuth or compatible credential", auth)
		}
	}
}

func TestClaudeExecutorSupportsAccountInfoKeepsMissingTokenPending(t *testing.T) {
	exec := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Provider: "claude", Metadata: map[string]any{"refresh_token": "refresh-only"}}
	if !exec.SupportsAccountInfo(auth) {
		t.Fatal("OAuth credential without access token would bypass account admission")
	}
	info, errFetch := exec.FetchAccountInfo(t.Context(), auth)
	if errFetch == nil || info != nil {
		t.Fatalf("FetchAccountInfo() = %#v, %v, want token acquisition to remain pending", info, errFetch)
	}
}
