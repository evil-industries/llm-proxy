package helps

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

const codexAccountUsageFixture = `{
	"plan_type":"pro", "account_id":"account-1", "user_id":"user-1",
	"rate_limit":{"allowed":true,"limit_reached":false,
		"primary_window":{"used_percent":12.5,"limit_window_seconds":18000,"reset_after_seconds":3600,"reset_at":1782954000},
		"secondary_window":{"used_percent":50,"limit_window_seconds":604800,"reset_at":1783000000}}
}`

func TestParseCodexAccountInfoUsesEarliestAvailableBankedReset(t *testing.T) {
	now := time.Unix(1782950000, 0)
	earliest := now.Add(20 * time.Minute)
	details, errMarshal := json.Marshal(map[string]any{
		"available_count": 4,
		"credits": []map[string]any{
			{"reset_type": "codex_rate_limits", "status": "available", "expires_at": now.Add(time.Hour).Format(time.RFC3339)},
			{"reset_type": "codex_rate_limits", "status": "redeemed", "expires_at": now.Add(time.Minute).Format(time.RFC3339)},
			{"reset_type": "another_limit", "status": "available", "expires_at": now.Add(time.Minute).Format(time.RFC3339)},
			{"reset_type": "codex_rate_limits", "status": "available", "expires_at": now.Add(-time.Minute).Format(time.RFC3339)},
			{"reset_type": "codex_rate_limits", "status": "available", "expires_at": earliest.Format(time.RFC3339)},
			{"reset_type": "codex_rate_limits", "status": "available", "expires_at": nil},
		},
	})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	info, errParse := ParseCodexAccountInfo([]byte(codexAccountUsageFixture), details, now)
	if errParse != nil {
		t.Fatal(errParse)
	}
	if !info.BankedResetAt.Equal(earliest) || !info.Quota.ObservedAt.Equal(now) {
		t.Fatalf("unexpected banked reset or observation time: %+v", info)
	}
	for name, want := range map[string]string{
		"X-Codex-Plan-Type": "pro", "X-Codex-Primary-Used-Percent": "12.5",
		"X-Codex-Primary-Window-Minutes": "300", "X-Codex-Secondary-Reset-At": "1783000000",
		"X-Codex-Rate-Limit-Reset-Credits-Available-Count": "4",
	} {
		if got := info.Quota.Signals[http.CanonicalHeaderKey(name)]; got != want {
			t.Errorf("signal %s = %q, want %q", name, got, want)
		}
	}
	if info.Metadata["plan_type"] != "pro" || info.Metadata["account_id"] != "account-1" || info.Metadata["user_id"] != "user-1" {
		t.Fatalf("unexpected metadata: %#v", info.Metadata)
	}
}

func TestParseCodexAccountInfoPreservesAdditionalAndReviewWindows(t *testing.T) {
	usage := strings.TrimSuffix(strings.TrimSpace(codexAccountUsageFixture), "}") + `,
		"code_review_rate_limit":{"primary_window":{"used_percent":2,"limit_window_seconds":604800,"reset_after_seconds":600}},
		"additional_rate_limits":[{"limit_name":"Codex-Spark","rate_limit":{"primary_window":{"used_percent":3,"limit_window_seconds":18000,"reset_after_seconds":900}}}]
	}`
	info, errParse := ParseCodexAccountInfo([]byte(usage), []byte(`{"available_count":0,"credits":[]}`), time.Unix(1782950000, 0))
	if errParse != nil {
		t.Fatal(errParse)
	}
	for name, want := range map[string]string{
		"X-Codex-Code-Review-Primary-Used-Percent":                   "2",
		"X-Codex-Code-Review-Primary-Reset-After-Seconds":            "600",
		"X-Codex-Additional-Codex-Spark-Primary-Used-Percent":        "3",
		"X-Codex-Additional-Codex-Spark-Primary-Reset-After-Seconds": "900",
	} {
		if got := info.Quota.Signals[http.CanonicalHeaderKey(name)]; got != want {
			t.Errorf("signal %s = %q, want %q", name, got, want)
		}
	}
}

func TestParseCodexAccountInfoRejectsIncompleteQueries(t *testing.T) {
	for _, tc := range []struct{ name, usage, details string }{
		{"invalid usage JSON", `{"plan_type":`, `{"available_count":0,"credits":[]}`},
		{"trailing usage JSON", codexAccountUsageFixture + `{}`, `{"available_count":0,"credits":[]}`},
		{"missing plan", strings.Replace(codexAccountUsageFixture, `"plan_type":"pro",`, "", 1), `{"available_count":0,"credits":[]}`},
		{"invalid plan", strings.Replace(codexAccountUsageFixture, `"pro"`, `"pro\nInjected: value"`, 1), `{"available_count":0,"credits":[]}`},
		{"missing regular windows", `{"plan_type":"pro","rate_limit":{"allowed":true}}`, `{"available_count":0,"credits":[]}`},
		{"invalid percentage", strings.Replace(codexAccountUsageFixture, `"used_percent":12.5`, `"used_percent":101`, 1), `{"available_count":0,"credits":[]}`},
		{"numeric string percentage", strings.Replace(codexAccountUsageFixture, `"used_percent":12.5`, `"used_percent":"12.5"`, 1), `{"available_count":0,"credits":[]}`},
		{"invalid window duration", strings.Replace(codexAccountUsageFixture, `"limit_window_seconds":18000`, `"limit_window_seconds":0`, 1), `{"available_count":0,"credits":[]}`},
		{"invalid reset", strings.Replace(codexAccountUsageFixture, `"reset_at":1782954000`, `"reset_at":"1782954000"`, 1), `{"available_count":0,"credits":[]}`},
		{"overflow relative reset", strings.Replace(codexAccountUsageFixture, `"reset_after_seconds":3600`, `"reset_after_seconds":9223372036854775807`, 1), `{"available_count":0,"credits":[]}`},
		{"missing reset count", codexAccountUsageFixture, `{"credits":[]}`},
		{"negative reset count", codexAccountUsageFixture, `{"available_count":-1,"credits":[]}`},
		{"fraction reset count", codexAccountUsageFixture, `{"available_count":1.5,"credits":[]}`},
		{"string reset count", codexAccountUsageFixture, `{"available_count":"1","credits":[]}`},
		{"invalid reset details JSON", codexAccountUsageFixture, `{"available_count":1`},
		{"missing credit array", codexAccountUsageFixture, `{"available_count":0}`},
		{"null credit array", codexAccountUsageFixture, `{"available_count":0,"credits":null}`},
		{"invalid credit entry", codexAccountUsageFixture, `{"available_count":1,"credits":[1]}`},
		{"malformed expiry", codexAccountUsageFixture, `{"available_count":1,"credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":"tomorrow"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if info, errParse := ParseCodexAccountInfo([]byte(tc.usage), []byte(tc.details), time.Unix(1782950000, 0)); errParse == nil || info != nil {
				t.Fatalf("incomplete account query accepted: info=%+v error=%v", info, errParse)
			}
		})
	}
}

func TestParseCodexAccountInfoAllowsNonexpiringAndCappedCreditDetails(t *testing.T) {
	for _, details := range []string{
		`{"available_count":3,"credits":[]}`,
		`{"available_count":3,"credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":null}]}`,
		`{"available_count":3,"credits":[{"reset_type":"codex_rate_limits","status":"available"}]}`,
	} {
		info, errParse := ParseCodexAccountInfo([]byte(codexAccountUsageFixture), []byte(details), time.Unix(1782950000, 0))
		if errParse != nil || !info.BankedResetAt.IsZero() {
			t.Fatalf("nonexpiring or capped details rejected: info=%+v error=%v", info, errParse)
		}
	}
}

const claudeAccountUsageFixture = `{"five_hour":{"utilization":35,"resets_at":"2026-07-02T12:00:00Z"},"seven_day":{"utilization":100,"resets_at":"2026-07-04T12:00:00Z"}}`

func TestParseClaudeAccountInfoNormalizesQueryAndCopiesMetadata(t *testing.T) {
	now := time.Unix(1782950000, 0)
	metadata := map[string]any{"account_uuid": "account-1", "organization_uuid": "organization-1"}
	info, errParse := ParseClaudeAccountInfo([]byte(claudeAccountUsageFixture), metadata, now)
	if errParse != nil {
		t.Fatal(errParse)
	}
	for name, want := range map[string]string{
		"Anthropic-Ratelimit-Unified-5h-Utilization": "0.35",
		"Anthropic-Ratelimit-Unified-5h-Status":      "allowed",
		"Anthropic-Ratelimit-Unified-5h-Reset":       "1782993600",
		"Anthropic-Ratelimit-Unified-7d-Utilization": "1",
		"Anthropic-Ratelimit-Unified-7d-Status":      "rejected",
	} {
		if got := info.Quota.Signals[http.CanonicalHeaderKey(name)]; got != want {
			t.Errorf("signal %s = %q, want %q", name, got, want)
		}
	}
	metadata["account_uuid"] = "changed"
	if info.Metadata["account_uuid"] != "account-1" || !info.Quota.ObservedAt.Equal(now) {
		t.Fatalf("unexpected account metadata or observation: %+v", info)
	}
}

func TestParseClaudeAccountInfoRejectsIncompleteQueries(t *testing.T) {
	for _, tc := range []struct{ name, usage string }{
		{"invalid JSON", `{"five_hour":`},
		{"missing five hour", `{"seven_day":{"utilization":10,"resets_at":"2026-07-04T12:00:00Z"}}`},
		{"null seven day", strings.Replace(claudeAccountUsageFixture, `{"utilization":100,"resets_at":"2026-07-04T12:00:00Z"}`, `null`, 1)},
		{"invalid percentage", strings.Replace(claudeAccountUsageFixture, `"utilization":35`, `"utilization":101`, 1)},
		{"numeric string percentage", strings.Replace(claudeAccountUsageFixture, `"utilization":35`, `"utilization":"35"`, 1)},
		{"missing reset", strings.Replace(claudeAccountUsageFixture, `,"resets_at":"2026-07-02T12:00:00Z"`, "", 1)},
		{"invalid reset", strings.Replace(claudeAccountUsageFixture, `2026-07-02T12:00:00Z`, `tomorrow`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, errParse := ParseClaudeAccountInfo([]byte(tc.usage), map[string]any{"account_uuid": "account-1"}, time.Unix(1782950000, 0))
			if errParse == nil || info != nil {
				t.Fatalf("incomplete account query accepted: info=%+v error=%v", info, errParse)
			}
		})
	}
	if info, errParse := ParseClaudeAccountInfo([]byte(claudeAccountUsageFixture), nil, time.Unix(1782950000, 0)); errParse == nil || info != nil {
		t.Fatalf("missing metadata accepted: info=%+v error=%v", info, errParse)
	}
}
