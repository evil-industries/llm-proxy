package helps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// IsAccountProbeBase restricts first-party account probes to credentials that
// route directly to the provider. Compatible gateways own their account data.
func IsAccountProbeBase(baseURL, provider string) bool {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return true
	}
	base, errParse := url.Parse(baseURL)
	if errParse != nil || !strings.EqualFold(base.Scheme, "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Port() != "" && base.Port() != "443") {
		return false
	}
	path := strings.TrimRight(base.Path, "/")
	switch provider {
	case "codex":
		return (strings.EqualFold(base.Hostname(), "chatgpt.com") || strings.EqualFold(base.Hostname(), "chat.openai.com")) && (path == "" || path == "/backend-api")
	case "claude":
		return strings.EqualFold(base.Hostname(), "api.anthropic.com") && (path == "" || path == "/v1")
	default:
		return false
	}
}

// FetchAccountProbeJSON uses the executor's existing proxy-aware transport and
// context without imposing a network deadline on an established connection.
func FetchAccountProbeJSON(ctx context.Context, auth *cliproxyauth.Auth, endpoint string, headers http.Header, request func(context.Context, *cliproxyauth.Auth, *http.Request) (*http.Response, error)) ([]byte, error) {
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if errRequest != nil {
		return nil, fmt.Errorf("create account information request: %w", errRequest)
	}
	req.Header = headers.Clone()
	resp, errDo := request(ctx, auth, req)
	if errDo != nil {
		return nil, fmt.Errorf("query account information: %w", errDo)
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("query account information: response body is missing")
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close account information response")
		}
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("query account information: status %d", resp.StatusCode)
	}
	const maxBodySize = 1024 * 1024
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if errRead != nil {
		return nil, fmt.Errorf("read account information: %w", errRead)
	}
	if len(body) > maxBodySize {
		return nil, fmt.Errorf("query account information: response exceeds size limit")
	}
	return body, nil
}
