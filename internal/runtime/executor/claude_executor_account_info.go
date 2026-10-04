package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func (e *ClaudeExecutor) SupportsAccountInfo(auth *cliproxyauth.Auth) bool {
	if e == nil || auth == nil || !strings.EqualFold(auth.Provider, "claude") {
		return false
	}
	token, baseURL := claudeCreds(auth)
	return (isClaudeOAuthToken(token) || auth.AuthKind() == cliproxyauth.AuthKindOAuth) && helps.IsAccountProbeBase(baseURL, "claude")
}

func (e *ClaudeExecutor) FetchAccountInfo(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.AccountInfo, error) {
	if !e.SupportsAccountInfo(auth) {
		return nil, fmt.Errorf("Claude account information requires first-party OAuth credentials")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	token, _ := claudeCreds(auth)
	if !isClaudeOAuthToken(token) {
		return nil, fmt.Errorf("fetch Claude account information: OAuth access token is missing")
	}
	// OAuth tokens supplied in api_key configuration still use Bearer auth for
	// account endpoints. Work on a copy so the model-request credential stays intact.
	probeAuth := auth.Clone()
	if probeAuth.Attributes == nil {
		probeAuth.Attributes = make(map[string]string)
	}
	delete(probeAuth.Attributes, "api_key")
	probeAuth.Attributes["auth_kind"] = cliproxyauth.AuthKindOAuth
	if probeAuth.Metadata == nil {
		probeAuth.Metadata = make(map[string]any)
	}
	probeAuth.Metadata["access_token"] = token
	headers := http.Header{
		"Accept":            {"application/json"},
		"Cache-Control":     {"no-cache"},
		"Content-Type":      {"application/json"},
		"User-Agent":        {"axios/1.15.2"},
		"Anthropic-Beta":    {"oauth-2025-04-20"},
		"Anthropic-Version": {"2023-06-01"},
	}
	observedAt := time.Now().UTC()
	usage, errUsage := helps.FetchAccountProbeJSON(ctx, probeAuth, "https://api.anthropic.com/api/oauth/usage", headers, e.HttpRequest)
	if errUsage != nil {
		return nil, fmt.Errorf("fetch Claude usage: %w", errUsage)
	}
	var profile *claudeauth.OAuthProfile
	if e.oauthProfileFetcher != nil {
		var errProfile error
		profile, errProfile = e.oauthProfileFetcher(ctx, probeAuth, token)
		if errProfile != nil {
			return nil, fmt.Errorf("fetch Claude account profile: %w", errProfile)
		}
	} else {
		body, errProfile := helps.FetchAccountProbeJSON(ctx, probeAuth, claudeauth.ProfileURL, headers, e.HttpRequest)
		if errProfile != nil {
			return nil, fmt.Errorf("fetch Claude account profile: %w", errProfile)
		}
		profile = &claudeauth.OAuthProfile{}
		if errParse := json.Unmarshal(body, profile); errParse != nil {
			return nil, fmt.Errorf("parse Claude account profile: %w", errParse)
		}
	}
	if profile == nil || uuid.Validate(strings.TrimSpace(profile.Account.UUID)) != nil {
		return nil, fmt.Errorf("fetch Claude account profile: valid account UUID is missing")
	}
	metadata := map[string]any{"account_uuid": strings.TrimSpace(profile.Account.UUID)}
	for key, value := range map[string]string{
		"email":             profile.Account.Email,
		"organization_uuid": profile.Organization.UUID,
		"organization_name": profile.Organization.Name,
	} {
		if value = strings.TrimSpace(value); value != "" {
			metadata[key] = value
		}
	}
	return helps.ParseClaudeAccountInfo(usage, metadata, observedAt)
}
