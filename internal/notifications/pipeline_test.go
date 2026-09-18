package notifications

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestLateRequestCompletionCannotInventBankedResetIncrease(t *testing.T) {
	for _, transport := range []string{"http", "websocket"} {
		for _, olderModel := range []string{"same-model", "other-model"} {
			t.Run(transport+"/"+olderModel, func(t *testing.T) {
				manager := auth.NewManager(nil, nil, nil)
				account, errRegister := manager.Register(context.Background(), &auth.Auth{ID: "account", Provider: "codex"})
				if errRegister != nil {
					t.Fatal(errRegister)
				}
				now := time.Unix(1787279282, 0)
				capture := func(count int, capturedAt time.Time) context.Context {
					ctx := logging.WithFreshResponseHeadersHolder(context.Background())
					if transport == "http" {
						logging.SetResponseHeadersAt(ctx, http.Header{"X-Codex-Rate-Limit-Reset-Credits-Available-Count": {fmt.Sprint(count)}}, capturedAt)
					} else {
						payload := []byte(fmt.Sprintf(`{"type":"codex.rate_limits","rate_limit_reset_credits":{"available_count":%d}}`, count))
						logging.MergeResponseHeadersAt(ctx, helps.ParseCodexQuotaEventHeaders(payload), capturedAt)
					}
					return ctx
				}
				older := capture(2, now.Add(-2*time.Minute))
				newer := capture(0, now.Add(-time.Minute))
				var sends atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1) }))
				defer server.Close()
				service := New(manager.List)
				service.now = func() time.Time { return now }
				cfg := testSettings(server.URL)
				cfg.BankedResetNotifications = true
				service.Configure(cfg)
				manager.MarkResult(newer, auth.Result{AuthID: account.ID, Provider: "codex", Model: "same-model", Success: true})
				service.poll(context.Background())
				manager.MarkResult(older, auth.Result{AuthID: account.ID, Provider: "codex", Model: olderModel, Success: true})
				service.poll(context.Background())
				if sends.Load() != 0 {
					t.Fatal("late completion invented available banked resets")
				}
				updated, _ := manager.GetByID(account.ID)
				if updated.Quota.Signals["X-Codex-Rate-Limit-Reset-Credits-Available-Count"] != "0" || !updated.Quota.ObservedAt.Equal(now.Add(-time.Minute)) {
					t.Fatalf("late completion replaced the fresh account snapshot: %+v", updated.Quota)
				}
			})
		}
	}
}

func TestCompletedRequestCannotRefreshStaleOrMissingWatermarks(t *testing.T) {
	now := time.Unix(1787279282, 0)
	for _, newerFrame := range []bool{false, true} {
		t.Run(fmt.Sprint(newerFrame), func(t *testing.T) {
			manager := auth.NewManager(nil, nil, nil)
			account, errRegister := manager.Register(context.Background(), &auth.Auth{ID: "account", Provider: "codex"})
			if errRegister != nil {
				t.Fatal(errRegister)
			}
			ctx := logging.WithFreshResponseHeadersHolder(context.Background())
			logging.SetResponseHeadersAt(ctx, http.Header{
				"X-Codex-Rate-Limit-Reset-Credits-Available-Count": {"2"},
				"X-Codex-Primary-Used-Percent":                     {"99"},
				"X-Codex-Primary-Reset-After-Seconds":              {"60"},
			}, now.Add(-ObservationMaxAge-time.Minute))
			if newerFrame {
				// A newer quota frame without a banked count must not refresh the old count.
				logging.MergeResponseHeadersAt(ctx, helps.ParseCodexQuotaEventHeaders([]byte(`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":2,"window_minutes":300,"reset_after_seconds":600}}}`)), now)
			}
			manager.MarkResult(ctx, auth.Result{AuthID: account.ID, Provider: "codex", Model: "model", Success: true})
			if got := bankedObservations(manager.List(), now); len(got) != 0 {
				t.Fatalf("stale/missing banked count became fresh: %+v", got)
			}
			got := quotaObservations(manager.List(), now)
			if (!newerFrame && len(got) != 0) || (newerFrame && (len(got) != 1 || got[0].remaining != 98)) {
				t.Fatalf("wrong quota observation after late completion: %+v", got)
			}
		})
	}
}
