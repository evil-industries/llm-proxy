package logging

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestWithFreshResponseHeadersHolderIsolatesAttempts(t *testing.T) {
	requestCtx := WithResponseHeadersHolder(context.Background())
	SetResponseHeaders(requestCtx, http.Header{"X-Upstream-Attempt": []string{"request"}})

	firstAttemptCtx := WithFreshResponseHeadersHolder(requestCtx)
	if headers := GetResponseHeaders(firstAttemptCtx); len(headers) != 0 {
		t.Fatalf("fresh attempt inherited request headers: %#v", headers)
	}
	SetResponseHeaders(firstAttemptCtx, http.Header{"X-Upstream-Attempt": []string{"first"}})

	secondAttemptCtx := WithFreshResponseHeadersHolder(firstAttemptCtx)
	if headers := GetResponseHeaders(secondAttemptCtx); len(headers) != 0 {
		t.Fatalf("second attempt inherited first attempt headers: %#v", headers)
	}
	SetResponseHeaders(secondAttemptCtx, http.Header{"X-Upstream-Attempt": []string{"second"}})

	if got := GetResponseHeaders(requestCtx).Get("X-Upstream-Attempt"); got != "request" {
		t.Fatalf("request holder = %q, want request", got)
	}
	if got := GetResponseHeaders(firstAttemptCtx).Get("X-Upstream-Attempt"); got != "first" {
		t.Fatalf("first attempt holder = %q, want first", got)
	}
	if got := GetResponseHeaders(secondAttemptCtx).Get("X-Upstream-Attempt"); got != "second" {
		t.Fatalf("second attempt holder = %q, want second", got)
	}
}

func TestResponseObservationRetainsCaptureTimeAndSnapshot(t *testing.T) {
	ctx := WithFreshResponseHeadersHolder(context.Background())
	initial := time.Unix(100, 0)
	SetResponseHeadersAt(ctx, http.Header{"X-Request-Id": {"request"}, "X-Codex-Primary-Used-Percent": {"99"}}, initial)
	headers, observedAt := GetResponseObservation(ctx)
	if !observedAt.Equal(initial) || headers.Get("X-Codex-Primary-Used-Percent") != "99" {
		t.Fatalf("initial observation = %v at %v", headers, observedAt)
	}
	headers.Set("X-Codex-Primary-Used-Percent", "0")
	frameTime := initial.Add(time.Minute)
	MergeResponseHeadersAt(ctx, http.Header{"X-Codex-Rate-Limit-Reset-Credits-Available-Count": {"2"}}, frameTime)
	headers, observedAt = GetResponseObservation(ctx)
	if !observedAt.Equal(frameTime) || headers.Get("X-Codex-Rate-Limit-Reset-Credits-Available-Count") != "2" || headers.Get("X-Codex-Primary-Used-Percent") != "" {
		t.Fatalf("frame inherited a stale watermark: %v at %v", headers, observedAt)
	}
	if merged := GetResponseHeaders(ctx); merged.Get("X-Request-Id") != "request" || merged.Get("X-Codex-Primary-Used-Percent") != "99" {
		t.Fatalf("merged log headers changed: %v", merged)
	}
	MergeResponseHeadersAt(ctx, http.Header{"X-Codex-Rate-Limit-Reset-Credits-Available-Count": {"0"}}, initial)
	MergeResponseHeadersAt(ctx, nil, frameTime.Add(time.Minute))
	headers, observedAt = GetResponseObservation(ctx)
	if !observedAt.Equal(frameTime) || headers.Get("X-Codex-Rate-Limit-Reset-Credits-Available-Count") != "2" {
		t.Fatalf("older or empty frame changed observation: %v at %v", headers, observedAt)
	}
	fresh := WithFreshResponseHeadersHolder(ctx)
	if headers, observedAt := GetResponseObservation(fresh); len(headers) != 0 || !observedAt.IsZero() {
		t.Fatal("fresh retry inherited the preceding observation")
	}
}
