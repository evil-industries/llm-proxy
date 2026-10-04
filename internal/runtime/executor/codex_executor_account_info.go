package executor

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func (e *CodexExecutor) SupportsAccountInfo(auth *cliproxyauth.Auth) bool {
	if e == nil || auth == nil || !strings.EqualFold(auth.Provider, "codex") || auth.AuthKind() != cliproxyauth.AuthKindOAuth || strings.TrimSpace(auth.Attributes["api_key"]) != "" {
		return false
	}
	_, baseURL := codexCreds(auth)
	return helps.IsAccountProbeBase(baseURL, "codex")
}

func (e *CodexExecutor) FetchAccountInfo(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.AccountInfo, error) {
	if !e.SupportsAccountInfo(auth) {
		return nil, fmt.Errorf("codex account information requires first-party OAuth credentials")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	token, _ := codexCreds(auth)
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("fetch Codex account information: access token is missing")
	}
	headers := http.Header{
		"Accept":        {"application/json"},
		"Cache-Control": {"no-cache"},
		"User-Agent":    {codexUserAgent},
		"Originator":    {codexOriginator},
	}
	if accountID, ok := auth.Metadata["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
		headers.Set("Chatgpt-Account-Id", strings.TrimSpace(accountID))
	}
	observedAt := time.Now().UTC()
	usage, errUsage := helps.FetchAccountProbeJSON(ctx, auth, "https://chatgpt.com/backend-api/wham/usage", headers, e.HttpRequest)
	if errUsage != nil {
		return nil, fmt.Errorf("fetch Codex usage: %w", errUsage)
	}
	credits, errCredits := helps.FetchAccountProbeJSON(ctx, auth, "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits", headers, e.HttpRequest)
	if errCredits != nil {
		return nil, fmt.Errorf("fetch Codex reset credits: %w", errCredits)
	}
	info, errParse := helps.ParseCodexAccountInfo(usage, credits, observedAt)
	if errParse != nil {
		return nil, errParse
	}
	configuredID, _ := auth.Metadata["account_id"].(string)
	queriedID, _ := info.Metadata["account_id"].(string)
	if strings.TrimSpace(configuredID) != "" && strings.TrimSpace(queriedID) != "" && strings.TrimSpace(configuredID) != strings.TrimSpace(queriedID) {
		return nil, fmt.Errorf("fetch Codex account information: response account identity does not match credential")
	}
	return info, nil
}

func (e *CodexAutoExecutor) SupportsAccountInfo(auth *cliproxyauth.Auth) bool {
	return e != nil && e.httpExec != nil && e.httpExec.SupportsAccountInfo(auth)
}

func (e *CodexAutoExecutor) FetchAccountInfo(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.AccountInfo, error) {
	if e == nil || e.httpExec == nil {
		return nil, fmt.Errorf("codex auto executor: http executor is nil")
	}
	return e.httpExec.FetchAccountInfo(ctx, auth)
}
