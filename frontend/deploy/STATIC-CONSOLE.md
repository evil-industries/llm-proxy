# Static console: single-container migration

This is a build/runbook, not a deployment script. It does not change DNS, Traefik, production files, provider accounts or fleet infrastructure. The production target is the ARM64 Debian 13 `cax21-maple` host. The image uses Debian bookworm userspace, which runs on that host's kernel; AMD64 is supported by the same Dockerfile.

## Build a committed revision

From the project root of the tested revision:

```sh
git rev-parse HEAD
docker buildx build --platform linux/arm64 --load \
  -f Dockerfile -t llm-proxy:static-console .
```

The root `Dockerfile` performs `npm ci` and the static build in a Node build stage, and a CGO-enabled Go build in a Go build stage. The final image contains the Go executable, CA certificates, timezone data, config example and `/CLIProxyAPI/console` static files. It contains no Node/npm frontend runtime, `node_modules`, SvelteKit server bundle or private environment/config files. Build with `--platform linux/amd64` for AMD64. To publish a multi-platform image use `--platform linux/arm64,linux/amd64 --push` with an authorized registry tag; `--load` normally accepts a single platform.

Do not pass OIDC secrets, management passwords, provider tokens or production `.env` files as build arguments. The same static assets work for both CPU architectures and auth modes. Image metadata may be set with the existing `VERSION`, `COMMIT`, and `BUILD_DATE` build args.

## Environment migration

Consolidate the **runtime environment** supplied by existing `backend.env` and `console.env` onto the Go container. Keep a private backup and preserve each value exactly. No production credentials are needed to build or test this revision.

| Existing source                                                                                                          | Action on the single Go container                                                                                                                                     |
| ------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `backend.env`: storage credentials, log-archive/AWS credentials, proxy settings, timezone and other Go service variables | Preserve all still-used backend variables. Do not move them into frontend build variables.                                                                            |
| `console.env`: `AUTH_MODE`, `ORIGIN`, `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_ALLOWED_USERS`        | Move unchanged into the Go runtime environment. OIDC client and callback registration remain unchanged.                                                               |
| `console.env`: `AUTH_PASSWORD_HASH`                                                                                      | Preserve only for password mode; the existing scrypt format is compatible. OIDC mode ignores it.                                                                      |
| `PRIVATE_MANAGEMENT_URL`, `PRIVATE_MANAGEMENT_KEY`                                                                       | Remove. There is no HTTP relay between containers anymore.                                                                                                            |
| `MANAGEMENT_PASSWORD`                                                                                                    | No longer required by the console. Remove if no local legacy management tool needs it; otherwise keep private. The console's outer guard still blocks remote `/v0/*`. |
| Node `HOST`, `PORT`, `BODY_SIZE_LIMIT`, `ADDRESS_HEADER`, `XFF_DEPTH`, `NODE_ENV`                                        | Remove Node-specific settings. Set Go listening host/port in YAML.                                                                                                    |
| Caddy-specific environment                                                                                               | Remove; no Caddy service remains.                                                                                                                                     |
| New settings                                                                                                             | Set `CONSOLE_ENABLED=true` and `CONSOLE_STATIC_DIR=/CLIProxyAPI/console`.                                                                                             |

For the current production identity configuration:

```dotenv
CONSOLE_ENABLED=true
CONSOLE_STATIC_DIR=/CLIProxyAPI/console
ORIGIN=https://llm.rose.sh
AUTH_MODE=oidc
OIDC_ISSUER=https://auth.rose.sh
OIDC_CLIENT_ID=llm-proxy-management
OIDC_ALLOWED_USERS=julius
```

Retain the actual existing client ID, allowed usernames and private client secret if they differ. Supply `OIDC_CLIENT_SECRET` from the existing secret store/environment file; do not paste it into a Dockerfile or public source. Missing/invalid auth settings disable the console with HTTP 503 while inference and health remain available. Auth configuration is loaded once at startup; restart to rotate it.

Environment-file parsers differ. Docker/Compose `env_file` with `format: raw` passes quotes and dollar signs literally; values in those files must be unquoted. Compose's standard `.env` parser strips single quotes, which are useful around scrypt hashes. Do not concatenate files blindly if they contain conflicting names, and never `source` secret files as shell code. If preserving two env files, apply the console auth values after backend values and explicitly override obsolete Node settings.

## Configuration and persistent volumes

Use the existing `runtime/config.yaml` contents, retaining provider/routing config, client API keys, notifications, usage metrics, log archival settings and storage backend settings. Do not replace it with `config.example.yaml`. Apply only these deployment-specific settings:

```yaml
host: '' # listen on the container network
port: 8317
tls:
  enable: false # Traefik terminates HTTPS
remote-management:
  allow-remote: false
  # Keep an existing secret-key only for local legacy tools; console does not need it.
  disable-control-panel: true
  disable-auto-update-panel: true
# Keep the existing auth-dir value if its volume mount is preserved.
# Example for a new mount target:
auth-dir: /var/lib/cliproxy/auths
```

Merge these fields into the existing sections; do not create duplicate YAML keys. A blank legacy `secret-key` disables the network management API without disabling the console's in-process operations. The custom console does not require `allow-remote: true`.

Preserve all existing data and **volume identities**:

- Mount the **runtime directory** containing `config.yaml` read/write, for example `./runtime:/etc/gateway`, and run `./CLIProxyAPI --config /etc/gateway/config.yaml`. Mounting the directory allows atomic config replacement by settings/API-key changes to persist. Match existing ownership and permissions.
- Reattach the existing **provider-accounts** volume at the path configured by `auth-dir`. For example, mount `provider-accounts:/var/lib/cliproxy/auths` and use that `auth-dir`. If the existing configuration uses `/root/.cli-proxy-api`, retain that path and mount instead. Changing the target requires changing `auth-dir` consistently. Never mount it under `/CLIProxyAPI/console`.
- Reattach the existing **logs** volume at its existing resolved path, commonly `/CLIProxyAPI/logs` with `WRITABLE_PATH=/CLIProxyAPI`. Retain archive metadata/spool files and `log-archive` credentials/settings. The log capture/archival overhead fix from `686155fc` is retained.
- Preserve any **plugins** volume, file/git/object/database stores, and required private mounts/network access. Existing backend configuration remains authoritative.
- Never declare a new empty Compose volume in place of the existing named volume. For a renamed service/project, use `external: true` and the exact existing `name:`. Do not run `down -v` during migration.

The sample `frontend/compose.yaml` uses one service and fresh-development volume names. Adapt mounts/network membership and explicit external volume names to your existing fleet; do not run it unmodified against production data.

## Port and Traefik routing

There is one application HTTP port: **8317**. No port 3000 or Caddy port is required. Do not expose 8317 directly to the Internet: connect Traefik to its private Docker network, or bind a host port to loopback when Traefik runs on the host. The Go listener can be plain HTTP behind Traefik; secure cookies are selected from the public `ORIGIN`, independent of the internal transport.

Forward the hostname `llm.rose.sh` directly to Go port 8317 and preserve the original Host header. A single hostname route covers:

| Public paths                                                                              | Behavior                                                                                                            |
| ----------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `/`, `/login`, navigation deep links                                                      | Static SPA shell; UI checks `/api/session` before showing management.                                               |
| `/_app/*`, built public assets                                                            | Static assets; immutable hashed assets are cacheable; missing assets return 404.                                    |
| `GET/POST/DELETE /api/session`                                                            | Session discovery, password sign-in and logout. Password POST is denied in OIDC mode.                               |
| `POST /auth/oidc/login`, `GET /auth/oidc/callback`                                        | Go-owned OIDC authentication with the unchanged public callback URI.                                                |
| `/api/management/*`                                                                       | Authenticated, method/path/query-restricted console management operations.                                          |
| `/api/codex/device-auth`                                                                  | Session-bound Codex login; no backend state identifier is exposed.                                                  |
| `/v1/*`, `/v1beta/*`, `/openai/v1/*`, `/backend-api/codex/*`, registered extension routes | Existing inference/API-key routing, SSE and WebSocket behavior.                                                     |
| `/healthz`                                                                                | Unauthenticated GET/HEAD health response, `200 {"status":"ok"}`.                                                    |
| `/v0/*`, `/management.html`                                                               | Remote access blocked by Go. Direct loopback `/v0` callers without forwarding headers still require the legacy key. |

Keep optional explicit Traefik deny rules for `/v0/*` and `/management.html` as defense in depth. Do not add blanket Authelia forward-auth to the inference host; the console performs its own OIDC login and inference retains API-key auth. Allow streaming responses without buffering and preserve WebSocket upgrades. Avoid response/request duration limits for inference, live updates and authenticated uploads/downloads. In-process forwarding adds no network timeout or application payload cap.

## Acceptance before switching production traffic

Use only temporary configuration and fixture accounts for local acceptance. Production acceptance belongs to the deploying operator:

1. Confirm the image architecture is `arm64` and that `node`, `npm` and `frontend/server.mjs` are absent.
2. Confirm `/healthz` is 200 and unauthenticated `/api/management/config` is 401.
3. Open `/login`, follow Authelia, and confirm only allowed usernames receive a Secure, HttpOnly, SameSite=Lax host-only session cookie. An unlisted user, forged/replayed callback, or password POST in OIDC mode must fail.
4. Confirm remote `/v0/management/config` and `/management.html` are 404 even with the old management key. Confirm cross-origin writes and spoofed identity/forwarding headers cannot authorize the console.
5. Verify dashboard, live throughput, settings, account list, credential upload/toggle/delete, API-key management, notifications, server/request logs, local/archive downloads, and device-login status. Sensitive logs/keys remain operator-only. Config and account read responses must omit provider tokens and private configuration.
6. Reload a navigation deep link; verify JS/CSS assets load. Confirm SSE changes arrive immediately and logout closes streams/revokes the cookie. Confirm inference JSON/SSE and WebSocket upgrades retain API-key protection.
7. Restart the container against the same mounts: expect to log in again, but retain configuration, client keys, provider accounts, log files and archive state. Rollback requires the prior image and the same preserved mounts/environment backup; no schema/data migration is performed by this change.

Automated Go/TLS fixtures and Chromium tests cover these contracts without production credentials. Actual Authelia/Traefik integration and VPS resource sizing must be checked by the operator after migration. The old separate console's 512 MiB runtime cap can be removed along with that container, but the Go container still needs memory for inference, logging and (password mode only) bounded scrypt checks. No new Go memory limit is prescribed here.
