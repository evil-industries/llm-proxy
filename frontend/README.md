# Management frontend

The custom SvelteKit 2 / Svelte 5 console is a **static SPA**. The same Go service serves its assets, authenticates operators, and exposes its scoped management API. Node.js is needed only to develop/build the assets. The default deployment is **one Go container**, behind Traefik's TLS termination, with no Caddy or Node runtime. The merged dashboard, live throughput, settings, credential management, log inspection and archival behavior are retained.

## Build and develop

```sh
cd frontend
npm ci
npm run build
npm run test:static
```

The deployable output is `frontend/build/`, including `index.html` and `/_app/immutable/` assets. Build it without production environment files or secrets.

For a local Go-served console, configure the Go process with `CONSOLE_ENABLED=true`, `CONSOLE_STATIC_DIR=frontend/build`, `ORIGIN=http://127.0.0.1:8317`, `AUTH_MODE=password` and `AUTH_PASSWORD_HASH`. Generate a compatible hash with `cd frontend && npm run auth:hash` (interactive input is hidden). Run `go run ./cmd/server --config <private-config.yaml> --local-model` from the project root and open the origin. A Go-only binary build does not automatically compile the assets.

For Vite development, run Go on 8317 with `ORIGIN=http://127.0.0.1:5173` and `npm run dev` from `frontend/`. Vite forwards `/api` and `/auth` to Go, preserving the browser Host. Set server variables on the Go process or in its root `.env`; a frontend `.env` is not a server configuration anymore. OIDC requires a reachable HTTPS origin/callback and trusted HTTPS issuer.

## Authentication and scope

`CONSOLE_ENABLED=true` opts into the custom console. Without it, the existing upstream-compatible API/control-panel behavior is unchanged. Set these **runtime server variables**, never `VITE_*` or public build variables:

| Variable                               | Purpose                                                                                                   |
| -------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `CONSOLE_ENABLED`                      | `true` enables the console and local-only legacy management guard.                                        |
| `CONSOLE_STATIC_DIR`                   | Public build directory; Docker defaults to `/CLIProxyAPI/console`, source runs to `frontend/build`.       |
| `ORIGIN`                               | Exact public HTTPS origin, e.g. `https://llm.rose.sh`; HTTP is accepted only on loopback for development. |
| `AUTH_MODE`                            | `password` (default) or `oidc`.                                                                           |
| `AUTH_PASSWORD_HASH`                   | Existing `scrypt$131072$8$1$...` password hash; required only in password mode.                           |
| `OIDC_ISSUER`                          | HTTPS issuer, e.g. `https://auth.rose.sh`.                                                                |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | Confidential OIDC client credentials; required in OIDC mode.                                              |
| `OIDC_ALLOWED_USERS`                   | Comma-separated, case-sensitive `preferred_username` allowlist; required in OIDC mode.                    |

Go handles authorization-code exchange with S256 PKCE, state and nonce checks, RS256 signature/issuer/audience/expiry validation, and subject-bound UserInfo username authorization. OIDC mode never falls back to password login. Login transactions expire after ten minutes and are one-use and browser-bound. Client secrets, provider tokens, PKCE verifiers and flow identifiers never reach the browser. See [Authelia setup](deploy/AUTHELIA.md).

Successful login issues an opaque, host-only, eight-hour cookie with `HttpOnly`, `SameSite=Lax`, `Path=/`, and HTTPS `Secure`/`__Host-` attributes. Go holds only hashed session handles; logout and expiry revoke in-flight console requests/SSE and cancel pending device login. Sessions and OIDC transactions are memory-only and lost on restart. Run one replica; there is no shared session store. Configuration/secret rotation requires a restart. Logout ends this console session; it does not end the Authelia SSO session.

Writes require the exact `Origin`; Host validation uses configured `ORIGIN`, never caller-supplied forwarded headers. Login bodies are capped at 4 KiB. Password checks, login attempts, sessions and transactions are bounded. Login throttling uses the TCP peer, so users behind one Traefik share its five-attempt/15-minute bucket; arbitrary `X-Forwarded-For` cannot bypass it. Successful login resets that peer's counter.

The authenticated `/api/management/*` surface retains the console's explicit method/path/query allowlist. It cannot access raw YAML, provider-key configuration, provider-file downloads, arbitrary API calls, plugins or the destructive usage queue. Config reads expose only UI settings; account reads omit filesystem paths, provider tokens and extension metadata. Intentional client API-key management still returns those keys to authorized operators. Logs and archives can contain prompts, responses and diagnostic secrets; access remains restricted to authenticated operators.

The legacy `/v0/management` API and its schemas remain unchanged. With the console enabled, an outer guard permits those routes only to direct loopback callers without forwarding headers; the existing management-key requirement remains. Remote raw-management access and `/management.html` are blocked even if a legacy `MANAGEMENT_PASSWORD` is present. The console dispatches its restricted operations in-process and requires neither that password nor `PRIVATE_MANAGEMENT_URL`/`PRIVATE_MANAGEMENT_KEY`. Leave `remote-management.allow-remote: false`; disable the old control-panel download. Home mode does not expose the custom console.

## One-container production and migration

Use [the migration runbook](deploy/STATIC-CONSOLE.md) for the exact `backend.env` + `console.env` + `runtime/config.yaml` + provider-account volume migration, ARM64 build, routing and acceptance commands. No production files are copied or modified by the build. The repository root `Dockerfile` builds both assets and Go and copies only static files and the Go binary into Debian. The `frontend/Dockerfile`, Node `server.mjs`, two-container console service, and Caddy configuration are retired.

`frontend/compose.yaml` is a one-service example. It binds only `127.0.0.1:8317` by default. For Traefik on a Docker network, connect this service to that existing network and target container port 8317 instead of publishing it. Preserve existing volume identities and mount paths when adapting the example; a newly named volume would be empty.

## External plain HTTP static server (optional)

Go serving the SPA and API on one origin is the default. To serve assets from nginx or another plain HTTP server, publish **only `frontend/build/`** as its document root and use an SPA fallback to `/index.html` for navigation (missing `/_app/*` must still return 404). Send `/api/session`, `/api/management/*`, `/api/codex/device-auth`, and `/auth/oidc/*` to the Go service with the original Host, under the **same public HTTPS origin**. Traefik can route these paths directly to Go. Inference and `/healthz` also go to Go. Keep `CONSOLE_ENABLED=true` and the same auth environment on Go; the static server gets no secrets. Never serve the checkout, `.svelte-kit`, runtime config, auth or log directories. Cross-origin SPA/API deployment is not supported.

## Runtime throughput

Overview and quota management retain their live throughput snapshots every five seconds while visible. Metrics cover completed upstream attempts in the last five minutes (or startup warmup); retries count separately. Aggregates are process-local, reset on restart, and never consume the usage queue. Generation speed uses streaming output tokens divided by generation time; unavailable timings display an em dash. Enable Usage statistics in Settings to collect metrics.

## Connect a Codex subscription

Device authentication is the primary account setup flow. On a fresh gateway, Overview shows **Connect Codex account**; Credentials always offers it so you can add more accounts.

1. Select **Connect Codex account** to request a one-time code.
2. Copy the code and open the approval page. Sign in with the ChatGPT account whose Codex subscription you want to connect, then enter the code.
3. Return to the console. The gateway polls for approval, saves the credentials, and registers the account for routing before reporting success.

Device code authentication must be enabled in your ChatGPT security settings or by your workspace administrator. The account must have access to Codex. See the [official authentication documentation](https://learn.chatgpt.com/docs/auth).

Device-login status changes arrive through the same SSE connection. The local expiry countdown does not make network requests. You can cancel a pending attempt, request another code after expiry, and connect additional accounts. Reloading the page or returning to Credentials resumes the pending attempt within the same console session. The gateway performs approval polling even while the page is closed; cancelling UI requests does not cancel account setup. Explicit cancellation or logout cancels a pending flow, unless credential saving has already completed. A Go restart loses console sessions and pending flows. Saved credentials persist in the auth volume.

The approval link always points to OpenAI's device approval page. Private device identifiers, token exchange values, and provider tokens remain server-side. Browser requests use `/api/codex/device-auth`, bound to the authenticated operator session; the backend flow identifier is never returned to the browser. Codex credential files are written with owner-only permissions.

## Inspect logs

**Logs → Server logs** shows the latest application log lines, then receives live change notifications over one authenticated Server-Sent Events (SSE) connection shared by the console. No UI polling is used. Full cursor batches are drained serially without waiting for another change event, with a **Catching up** indicator until the backlog is exhausted. Pause remains available during catch-up; loaded history stays paginated in the browser. Pause/resume, refresh, search, severity filters, wrapping, line-detail dialogs, copy, and export apply to the loaded history. History is displayed in pages of 200 lines. **Load latest (replace history)** explicitly discards the browser's loaded history and reads the current tail. Rotation or truncation inserts a visible boundary and pauses updates so you can review possible overlap.

**Logs → Request logs** lists saved request and error files with timestamps and sizes. Filter by filename or look up an exact request ID, open a paged text preview, copy the loaded text, or download the full file. Preview reads use 64 KiB pages; full downloads stream without an application file-size cap. File changes during preview require a reload to avoid mixing versions.

Application file logging can be enabled from Logs. Full request logging is controlled in Settings and is disabled by default; error files may still be available according to the gateway's logging configuration. These files can include full prompts, responses, and diagnostic information, so protect and back up the logs volume accordingly. This interface inspects the gateway's existing files; it does not implement the proposed database conversation archive or analyses by API key.

## Quota notifications

The gateway can send quota alerts to a self-hosted [ntfy](https://ntfy.sh/) server. Notifications are disabled by default. In the console's notification settings, enter your ntfy base URL, topic, and optional access token with publish permission, then save and enable delivery. Subscribe to the same topic in your ntfy client. Use ntfy's access controls to restrict who can publish to and read the topic.

Warning and critical thresholds are percentages of remaining quota; defaults are 20% and 5%. Alerts depend on quota observations available to the gateway. The settings show delivery status, and the test button sends a real notification using the saved settings. Saving settings does not send a test. The ntfy token remains in the Go configuration and is never returned to the browser; leaving the token field unchanged preserves it, and removing it requires the explicit clear-token action.

The Go worker checks observations every 30 seconds and continues while the browser is closed. It currently recognizes numeric Codex and Claude quota windows observed during requests. It does not poll providers for new quota data: missing signals, observations older than 15 minutes, and expired quota windows cannot trigger alerts. Other providers are not covered yet.

Reset notifications and Codex banked-reset notifications default to enabled within the master notification switch, and can be disabled independently. A reset alert requires a newer observation confirming a later quota window; an elapsed timer alone does not send one. Recovery alerts also fire when a low-quota account recovers above the warning threshold plus five percentage points, including recovery before a scheduled reset. The first healthy observation establishes a baseline without sending a reset alert.

Banked resets are available reset grants, separate from monetary credits. The gateway alerts on the first explicitly reported positive count and on subsequent count increases, without redeeming any reset. Missing counts remain unknown; decreases and zero counts update the baseline silently. OpenAI's [app-server documentation](https://learn.chatgpt.com/docs/app-server#6-rate-limits-chatgpt) defines `rateLimitResetCredits.availableCount` as authoritative. The gateway accepts the corresponding snake/camel-case field in observed `codex.rate_limits` frames and its normalized `X-Codex-Rate-Limit-Reset-Credits-Available-Count` header. This is passive compatibility support: the app-server schema does not guarantee that your upstream inference transport exposes the field. Accounts whose responses omit it cannot trigger banked-reset alerts. The gateway does not connect to the Codex app server or actively query reset grants.

Alert suppression and delivery status live in memory. Restarts, configuration changes, or multiple gateway replicas can produce repeated alerts. Failed deliveries are retried from current observations with backoff, but there is no durable notification queue. A saved destination can be tested while automatic alerts remain disabled.

The Go management API exposes `GET /v0/management/notifications`, `PUT /v0/management/notifications`, and `POST /v0/management/notifications/test`, protected by the existing management authentication. General configuration JSON omits the notification settings. The YAML `notifications` section in the repository's `config.example.yaml` provides the equivalent server configuration.

## Components and stories

```sh
npm run garden        # http://127.0.0.1:3010
npm run garden:check  # require a story for every production Svelte component
```

Each production `.svelte` file has a neighboring GardenJS `.das.js` story. `.example.svelte` files are story-only fixtures, including wrappers for snippet-based shadcn components. Garden shares the application's CSS and provides phone, tablet, and desktop presets. Stories cover populated, empty, error, disabled, and long-content states where relevant. Add or update stories whenever a component changes. Interactive examples use in-memory fixtures and do not call the production backend.

Route stories omit the reserved `+` prefix (`page.das.js` and `layout.das.js`). Use `GARDEN_PORT=3012 npm run garden` if the default port is occupied. Garden is pinned to 1.6.2 to avoid the unused, vulnerable legacy `build` dependency in later releases; the `tmp` override patches its editor dependency. The `cookie` override supplies the patched serializer used by SvelteKit. Both the explorer and application use Svelte 5. A narrowly scoped Vite transform repairs invalid import bindings generated by Garden 1.6.2 for hyphenated directories such as `scroll-area`; it preserves component paths and story labels and has regression coverage.

Primitives and charts in `src/lib/components/ui/` follow [shadcn-svelte](https://www.shadcn-svelte.com/docs/installation/sveltekit). `components.json` preserves the registry configuration. Charts use server-provided request-history buckets; they do not fabricate historical values or consume the usage queue. Story syntax follows the [GardenJS documentation](https://gardenjs.org/docs/get-started/examples).

## Validation

```sh
npm ci
npx playwright install chromium
npm run check
npm run test:unit
npm run test:browser
npm run build
npm run test:static
# From the project root:
go test ./...
go test -race ./internal/console ./internal/api ./internal/api/handlers/management
go build -o test-output ./cmd/server && rm test-output
```

Go tests exercise real TLS OIDC discovery, signature/audience/issuer/expiry/nonce validation, PKCE and client authentication, username and subject checks, one-use browser-bound callbacks, logout and expiry, CSRF, upload streaming, console route restrictions, account/config secret filtering, log/archive downloads, SSE revocation, and WebSocket/inference pass-through. Server integration tests exercise the actual management handlers and persistence with temporary configuration and credentials. They do not contact production services.

Vitest keeps the API contracts and Chromium component, layout, accessibility, settings, credentials, notifications, logs, archive, and throughput tests. The static-output check requires the SPA fallback and immutable assets, rejects Node/server entrypoints and server environment markers, and checks supplied dummy secret values for accidental inclusion. SvelteKit's temporary `.svelte-kit/output/server` directory is a build intermediate and is never shipped; only `frontend/build/` is public.

The obsolete adapter-node, Node server/relay tests, Caddy and two-runtime Compose setup/smoke scripts have been replaced by Go tests and the single-container packaging. `garden:check` remains available for story coverage; some existing dashboard and primitive components still lack stories.

## API behavior

- Credential counters use `success` / `failed` totals returned by `/auth-files`. Charts use the credential files' recent-request history returned by the backend.
- The console never reads `/usage-queue`, because that endpoint consumes queued records.
- Data refresh is explicit. Section failures remain visible and preserve previously loaded data with a stale-data notice.
- Credential and client-key deletion require in-page confirmation. The final client API key cannot be deleted from the console, preserving API authentication. Client-key changes are atomic and identify the key in the request body. Mutations await a fresh server read after success, including when another refresh is already running.
- Codex device authentication is available directly in the console. Other provider OAuth flows, provider API-key configuration, plugin administration, and raw YAML editing remain available through existing management tools.

### Live usage and account updates

**Usage** and the overview show every account's provider-reported allowances, remaining percentages, and reset timestamps in your local timezone. Credentials also show these limits alongside each account. Codex and Claude usage windows are read from passive observations already captured by the gateway; additional Codex pools, credits, and banked resets appear when reported. Unsupported or unobserved limits are explicitly unavailable. Observations older than 15 minutes are marked stale, and a passed reset does not invent a replenished allowance.

The Go management `/events` endpoint publishes changes after account updates, requests, configuration changes, device-login completion, and log writes. The console relays it through the existing authenticated session without exposing the management key. Reconnection refreshes snapshots and resumes log cursors; logout and session expiry close the stream. Heartbeats keep the streaming transport open and never query state. Deploy the updated gateway and console together, and ensure the reverse proxy allows unbuffered SSE responses.
