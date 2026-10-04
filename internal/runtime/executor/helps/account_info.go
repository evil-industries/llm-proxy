package helps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// ParseCodexAccountInfo validates queried usage and reset-credit details before
// making them available to routing. Passive quota frames cannot satisfy this query.
func ParseCodexAccountInfo(usage, details []byte, observedAt time.Time) (*cliproxyauth.AccountInfo, error) {
	root, errUsage := decodeAccountInfoObject(usage)
	if errUsage != nil {
		return nil, fmt.Errorf("codex account usage: %w", errUsage)
	}
	plan, okPlan := root["plan_type"].(string)
	plan = strings.TrimSpace(plan)
	if !okPlan || !validCodexQuotaEventText(plan) {
		return nil, fmt.Errorf("codex account usage: missing or invalid plan_type")
	}
	metadata := map[string]any{"plan_type": plan}
	for _, name := range []string{"account_id", "user_id", "email"} {
		if value, exists := root[name]; exists && value != nil {
			text, okText := value.(string)
			if !okText || !validCodexQuotaEventText(strings.TrimSpace(text)) {
				return nil, fmt.Errorf("codex account usage: invalid %s", name)
			}
			metadata[name] = strings.TrimSpace(text)
		}
	}
	headers := make(http.Header)
	headers.Set("X-Codex-Plan-Type", plan)
	if errLimits := addQueriedCodexRateLimitHeaders(headers, "X-Codex-", root["rate_limit"]); errLimits != nil {
		return nil, fmt.Errorf("codex account usage: rate_limit: %w", errLimits)
	}
	if value := root["code_review_rate_limit"]; value != nil {
		if errReview := addQueriedCodexRateLimitHeaders(headers, "X-Codex-Code-Review-", value); errReview != nil {
			return nil, fmt.Errorf("codex account usage: code_review_rate_limit: %w", errReview)
		}
	}
	if value := root["additional_rate_limits"]; value != nil {
		limits, okLimits := value.([]any)
		if !okLimits {
			return nil, fmt.Errorf("codex account usage: invalid additional_rate_limits")
		}
		for index, value := range limits {
			limit, okLimit := value.(map[string]any)
			if !okLimit {
				return nil, fmt.Errorf("codex account usage: invalid additional_rate_limits entry %d", index)
			}
			name, okName := limit["limit_name"].(string)
			name = strings.TrimSpace(name)
			identifier := normalizeCodexQuotaHeaderIdentifier(name)
			if !okName || !validCodexQuotaEventText(name) || identifier == "" {
				return nil, fmt.Errorf("codex account usage: invalid additional_rate_limits name %d", index)
			}
			prefix := codexQuotaAdditionalHeaderKey + identifier + "-"
			if errLimit := addQueriedCodexRateLimitHeaders(headers, prefix, limit["rate_limit"]); errLimit != nil {
				return nil, fmt.Errorf("codex account usage: additional_rate_limits entry %d: %w", index, errLimit)
			}
			headers.Set(prefix+"Limit-Name", name)
		}
	}
	banked, count, errDetails := parseCodexResetCreditDetails(details, observedAt)
	if errDetails != nil {
		return nil, fmt.Errorf("codex account reset credits: %w", errDetails)
	}
	headers.Set(codexResetCreditsCountHeader, strconv.FormatInt(count, 10))
	info := &cliproxyauth.AccountInfo{Metadata: metadata, BankedResetAt: banked}
	info.Quota.ObserveResponseHeadersForProvider("codex", headers, observedAt)
	return info, nil
}

// ParseClaudeAccountInfo validates both shared usage windows and queried
// credential metadata, normalizing the percentage values to response headers.
func ParseClaudeAccountInfo(usage []byte, metadata map[string]any, observedAt time.Time) (*cliproxyauth.AccountInfo, error) {
	if len(metadata) == 0 {
		return nil, fmt.Errorf("claude account metadata: missing metadata")
	}
	root, errUsage := decodeAccountInfoObject(usage)
	if errUsage != nil {
		return nil, fmt.Errorf("claude account usage: %w", errUsage)
	}
	headers := make(http.Header)
	for _, window := range []struct{ name, header string }{{"five_hour", "5h"}, {"seven_day", "7d"}} {
		value, okWindow := root[window.name].(map[string]any)
		if !okWindow {
			return nil, fmt.Errorf("claude account usage: missing or invalid %s", window.name)
		}
		used, okUsed := accountInfoPercentage(value["utilization"])
		reset, okReset := value["resets_at"].(string)
		if !okReset {
			reset, okReset = value["reset_at"].(string)
		}
		resetAt, errReset := time.Parse(time.RFC3339, reset)
		if !okUsed || !okReset || errReset != nil || resetAt.Unix() <= 0 {
			return nil, fmt.Errorf("claude account usage: incomplete or invalid %s window", window.name)
		}
		prefix := "Anthropic-Ratelimit-Unified-" + window.header + "-"
		headers.Set(prefix+"Utilization", strconv.FormatFloat(used/100, 'f', -1, 64))
		headers.Set(prefix+"Reset", strconv.FormatInt(resetAt.Unix(), 10))
		status := "allowed"
		if used == 100 {
			status = "rejected"
		}
		headers.Set(prefix+"Status", status)
	}
	copyMetadata := make(map[string]any, len(metadata))
	for key, value := range metadata {
		copyMetadata[key] = value
	}
	info := &cliproxyauth.AccountInfo{Metadata: copyMetadata}
	info.Quota.ObserveResponseHeadersForProvider("claude", headers, observedAt)
	return info, nil
}

func decodeAccountInfoObject(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var object map[string]any
	if errDecode := decoder.Decode(&object); errDecode != nil {
		return nil, fmt.Errorf("invalid JSON object")
	}
	if object == nil {
		return nil, fmt.Errorf("missing JSON object")
	}
	var extra any
	if errExtra := decoder.Decode(&extra); errExtra != io.EOF {
		return nil, fmt.Errorf("invalid trailing JSON data")
	}
	return object, nil
}

func accountInfoPercentage(value any) (float64, bool) {
	number, okNumber := value.(json.Number)
	if !okNumber {
		return 0, false
	}
	percent, errParse := number.Float64()
	return percent, errParse == nil && !math.IsNaN(percent) && !math.IsInf(percent, 0) && percent >= 0 && percent <= 100
}

func accountInfoInteger(value any, minimum int64) (int64, bool) {
	number, okNumber := value.(json.Number)
	if !okNumber {
		return 0, false
	}
	integer, errParse := number.Int64()
	return integer, errParse == nil && integer >= minimum
}

func addQueriedCodexRateLimitHeaders(headers http.Header, prefix string, value any) error {
	limit, okLimit := value.(map[string]any)
	if !okLimit {
		return fmt.Errorf("missing or invalid limit")
	}
	for _, field := range []struct{ name, header string }{{"allowed", "Allowed"}, {"limit_reached", "Limit-Reached"}} {
		if value, exists := limit[field.name]; exists {
			flag, okFlag := value.(bool)
			if !okFlag {
				return fmt.Errorf("invalid %s", field.name)
			}
			headers.Set(prefix+field.header, strconv.FormatBool(flag))
		}
	}
	windows := 0
	for _, name := range []string{"primary", "secondary"} {
		value := limit[name+"_window"]
		if value == nil {
			continue
		}
		window, okWindow := value.(map[string]any)
		if !okWindow {
			return fmt.Errorf("invalid %s_window", name)
		}
		used, okUsed := accountInfoPercentage(window["used_percent"])
		seconds, okSeconds := accountInfoInteger(window["limit_window_seconds"], 1)
		resetAt, okResetAt := accountInfoInteger(window["reset_at"], 1)
		resetAfter, okResetAfter := accountInfoInteger(window["reset_after_seconds"], 0)
		if !okUsed || !okSeconds || (!okResetAt && !okResetAfter) {
			return fmt.Errorf("incomplete or invalid %s_window", name)
		}
		if value := window["reset_at"]; value != nil && !okResetAt {
			return fmt.Errorf("invalid %s_window reset_at", name)
		}
		if value := window["reset_after_seconds"]; value != nil && (!okResetAfter || resetAfter > int64(time.Duration(math.MaxInt64)/time.Second)) {
			return fmt.Errorf("invalid %s_window reset_after_seconds", name)
		}
		windowPrefix := prefix + strings.ToUpper(name[:1]) + name[1:] + "-"
		headers.Set(windowPrefix+"Used-Percent", strconv.FormatFloat(used, 'f', -1, 64))
		headers.Set(windowPrefix+"Window-Minutes", strconv.FormatFloat(float64(seconds)/60, 'f', -1, 64))
		if okResetAt {
			headers.Set(windowPrefix+"Reset-At", strconv.FormatInt(resetAt, 10))
		}
		if okResetAfter {
			headers.Set(windowPrefix+"Reset-After-Seconds", strconv.FormatInt(resetAfter, 10))
		}
		windows++
	}
	if windows == 0 {
		return fmt.Errorf("missing quota windows")
	}
	return nil
}

func parseCodexResetCreditDetails(payload []byte, observedAt time.Time) (time.Time, int64, error) {
	root, errDecode := decodeAccountInfoObject(payload)
	if errDecode != nil {
		return time.Time{}, 0, errDecode
	}
	count, okCount := accountInfoInteger(root["available_count"], 0)
	if !okCount {
		return time.Time{}, 0, fmt.Errorf("missing or invalid available_count")
	}
	credits, okCredits := root["credits"].([]any)
	if !okCredits {
		return time.Time{}, 0, fmt.Errorf("missing or invalid credits")
	}
	var earliest time.Time
	for index, value := range credits {
		credit, okCredit := value.(map[string]any)
		if !okCredit {
			return time.Time{}, 0, fmt.Errorf("invalid credit entry %d", index)
		}
		resetType, okType := credit["reset_type"].(string)
		status, okStatus := credit["status"].(string)
		if !okType || !okStatus || strings.TrimSpace(resetType) == "" || strings.TrimSpace(status) == "" {
			return time.Time{}, 0, fmt.Errorf("invalid credit type or status at entry %d", index)
		}
		if resetType != "codex_rate_limits" || status != "available" || credit["expires_at"] == nil {
			continue
		}
		expires, okExpires := credit["expires_at"].(string)
		expiresAt, errExpires := time.Parse(time.RFC3339, expires)
		if !okExpires || errExpires != nil {
			return time.Time{}, 0, fmt.Errorf("invalid available credit expiry at entry %d", index)
		}
		if count > 0 && expiresAt.After(observedAt) && (earliest.IsZero() || expiresAt.Before(earliest)) {
			earliest = expiresAt
		}
	}
	return earliest, count, nil
}
