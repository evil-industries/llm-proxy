package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	cliproxysession "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestQuotaResetAffinityUsesLastMessageLifetime(t *testing.T) {
	t.Parallel()

	for _, identity := range []string{"explicit", "conversation prefix"} {
		t.Run(identity, func(t *testing.T) {
			current := time.Now().Truncate(time.Second)
			const ttl = time.Minute
			selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{Fallback: &RoundRobinSelector{}, TTL: ttl})
			defer selector.Stop()
			selector.cache.Stop()
			selector.cache = &SessionCache{ttl: ttl, nowFunc: func() time.Time { return current }}
			selector.matcher = cliproxysession.NewMerklePrefixMatcherWithConfig(cliproxysession.MerklePrefixMatcherConfig{
				TTL: ttl, NowFunc: func() time.Time { return current },
			})
			manager := NewManager(nil, selector, nil)
			later := resetTestAuth("later", current, 2*time.Hour)
			sooner := resetTestAuth("sooner", current, time.Hour)
			options := func(thread string) cliproxyexecutor.Options {
				if identity == "explicit" {
					return cliproxyexecutor.Options{Headers: map[string][]string{"Session-Id": {thread}}}
				}
				return cliproxyexecutor.Options{
					SourceFormat:    sdktranslator.FormatOpenAI,
					OriginalRequest: []byte(`{"messages":[{"role":"user","content":"` + thread + `"}]}`),
					Metadata:        map[string]any{cliproxyexecutor.CallerScopeMetadataKey: "quota-lifetime-test"},
				}
			}
			pick := func(thread string, auths ...*Auth) string {
				t.Helper()
				preferred, candidates, errAvailable := manager.availableAuthsForSelector(selector, auths, "codex", "", current)
				if errAvailable != nil {
					t.Fatal(errAvailable)
				}
				ctx := selectorContextForAvailableAuths(context.Background(), selector, "", preferred)
				picked, errPick := selector.Pick(ctx, "codex", "", options(thread), candidates)
				if errPick != nil {
					t.Fatal(errPick)
				}
				if picked == nil {
					t.Fatal("selector returned no account")
				}
				return picked.ID
			}

			if got := pick("existing-thread", later); got != later.ID {
				t.Fatalf("initial binding = %q, want %q", got, later.ID)
			}
			if got := pick("new-thread", later, sooner); got != sooner.ID {
				t.Fatalf("new thread = %q, want nearest reset account %q", got, sooner.ID)
			}
			current = current.Add(ttl)
			if got := pick("existing-thread", later, sooner); got != later.ID {
				t.Fatalf("message at cache lifetime = %q, want bound account %q", got, later.ID)
			}
			current = current.Add(ttl)
			if got := pick("existing-thread", later, sooner); got != later.ID {
				t.Fatalf("message after refreshed lifetime = %q, want bound account %q", got, later.ID)
			}
			current = current.Add(ttl + time.Nanosecond)
			if got := pick("existing-thread", later, sooner); got != sooner.ID {
				t.Fatalf("inactive thread = %q, want nearest reset account %q", got, sooner.ID)
			}
		})
	}
}
