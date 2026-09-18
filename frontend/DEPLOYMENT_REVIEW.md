# Deployment review — 2026-09-18

The Caddy, Node console, and Go gateway stack passed local container verification. No remaining actionable blocker was found for this deployment scope. Deployment to the target infrastructure has not been performed.

## Device authentication and log inspection review

Codex subscription device authentication is now the primary console setup flow. It reuses the CLI protocol, binds private flow identifiers to the operator session, polls on the Go server, and persists/registers credentials before reporting completion. Log inspection includes application tailing and saved request/error files, paged UTF-8 previews, and streaming downloads.

Independent backend, session-security, and log-flow reviews found and resolved:

- Logout/cancellation races in the Node flow store and shutdown fencing for in-flight backend starts.
- Codex token files inheriting permissive creation modes; new and rewritten files now use `0600` before token writes.
- Log classification excluding legitimate Gemini thinking suffixes, punctuation, and Unicode; real logger-generated names now work through list, preview, and download, with separators and symlink escapes rejected.
- shadcn registry selectors differing from the installed Bits UI attributes, affecting Tabs and Switch appearance.
- Narrow-screen log layout and desktop tab wrapping, with browser geometry and visual checks.

The final pre-main review also identified and fixed two P2 issues:

- Device shutdown first waited indefinitely on the credential commit mutex; a follow-up correction returned too early during healthy commits. Shutdown now cancels acquisition promptly, then waits for active workers only within its deadline. Active workers are tracked separately from expiring public statuses. The service creates its 30-second grace period when shutdown begins. Regressions cover successful commit draining, deadline expiry while a commit remains blocked, cancellation of another acquisition, rejection of new starts, and retained tracking after public status cleanup.
- The live log viewer fetched only 200 lines every five seconds. It now drains successive cursor batches serially, yields between batches for interaction, shows **Catching up**, and allows pause/resume during catch-up. Browser regressions cover a 2,501-line backlog, single-flight reads, pause/resume, recovery after errors, and nonadvancing cursor protection.

No remaining actionable blocker was found for these features. No real provider account was authenticated during validation.

## Earlier deployment fixes (2026-09-17)

- Retained Caddy's `NET_BIND_SERVICE` capability: the official image failed to execute with all capabilities dropped, even with its listener on port 8080.
- Removed implicit five-minute management upload/response deadlines while retaining connection acquisition and HTTP-header protection.
- Updated the vulnerable cookie serializer, Go Git/SSH dependencies, and compression dependency. Adjusted Git corruption detection for the patched library's missing-pack errors without treating missing configuration or credential files as corruption.
- Corrected two scheduling-sensitive WebSocket tests using explicit synchronization.
- Added an isolated, repeatable Compose smoke test and a deployment CI workflow.

## Verification

The final shutdown-drain correction passed management/API race tests, production compilation, and independent actual-token-write reproductions for both expired and live grace periods. Both images were rebuilt and the complete Compose integration checks below passed again. Compose gives Go 35 seconds to stop so Docker permits the application’s 30-second grace period.

- Both final production images built successfully.
- The actual Caddy/Node/Go stack passed login, secure-cookie attributes, inference API-key enforcement, private management routing, cross-origin write rejection, authenticated management access, and logout revocation.
- JSON inference and progressive SSE passed through Caddy and Go against a local provider fixture. No live provider accounts were used.
- Codex device approval and token exchange passed through Caddy, Node, and Go using an ephemeral local HTTPS fixture. The fixture serves only the expected provider authentication endpoints and opens no outbound connections. Public responses omit private flow identifiers and token material.
- Management configuration writes and device-authenticated credentials persisted through a Go container restart.
- Real inference generated request logs; paged Unicode previews matched complete streamed downloads, and unauthenticated log access was rejected.
- 295 frontend tests passed, including session lifecycle, API contracts, keyboard interaction, WCAG checks, and clipping/overflow at narrow widths and enlarged text. Type checks, all 60 component stories, formatting, ten bootstrap tests, the built-server smoke test, and Caddy routing/WebSocket tests passed.
- Garden rendered the new components successfully; Playwright visual inspection covered device approval, live logs, the severity menu, and request-detail dialogs at desktop and phone widths.
- Full Go tests and required server compilation passed. Device protocol, SDK authentication, management lifecycle, and log-inspection race tests passed after the review fixes. Earlier store race tests and 50 race-detector repetitions of the corrected WebSocket tests also passed.
- The earlier dependency review reported zero npm vulnerabilities. Go source scanning found no reachable vulnerabilities. The stripped Linux release binary scan reported an OpenPGP module advisory using synthetic wildcard symbols: govulncheck falls back to module-level findings when symbols are stripped. The Linux dependency graph excludes the flagged `golang.org/x/crypto/openpgp` package and uses the maintained ProtonMail fork. This was assessed as a scanner precision limitation, not a discovered application call path.

The earlier Docker cleanup reclaimed 42.78 GB from unused images. Existing containers and data volumes were preserved. Smoke-test containers, networks, volumes, and temporary credentials were removed afterward.

## Rollout boundaries

Configure the actual HTTPS origin, outer reverse-proxy upstream, and trusted proxy source addresses as described in [README.md](README.md). Verify HTTPS login and one real provider request on the target infrastructure after connecting a Codex subscription or importing other provider credentials. The local tests do not validate that infrastructure, its certificates, or live provider quota/reset signals.

Configuration and provider credentials persist on disk/volumes. Console sessions and notification suppression remain in memory. The conversation archive database and database-backed authentication described in [CONVERSATION_RECORDING.md](../CONVERSATION_RECORDING.md) are still design work, not implemented features of this stack.

Repeat the container verification with `npm run test:compose`; it builds an isolated stack, uses a local provider fixture, and cleans up its own runtime resources.
