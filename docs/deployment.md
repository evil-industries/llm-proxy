# Deployment

[README](../README.md) · [API usage](api-usage.md) · [Configuration reference](../config.example.yaml)

CLIProxyAPI accepts client API requests, selects a configured provider account, and translates requests and responses between supported protocols. You need both a client API key for callers and at least one provider credential to serve model requests.

| Deployment | Use it for | Requirements |
| --- | --- | --- |
| [Gateway with console](#gateway-with-console) | A shared gateway with browser-based administration | Docker Compose, Node.js 22.12+ for setup, an HTTPS reverse proxy |
| [Go server](#go-server) | Local use or an API-only service | Go 1.26+, a C toolchain for CGO |
| [Go-only Docker Compose](#go-only-docker-compose) | An API-only container deployment | Docker Compose |

## Gateway with console

Run these commands from a fresh checkout, replacing the hostname with your public HTTPS origin:

```sh
cd frontend
node scripts/setup-compose.mjs https://gateway.example.com
docker compose config --quiet
docker compose up --build -d
```

The setup command requires no npm install. It prompts for a console password and generates separate management and client API keys. It creates `frontend/.env` and `frontend/deploy/data/config/config.yaml` with private permissions and refuses to overwrite an existing setup.

Point your HTTPS reverse proxy at `http://127.0.0.1:8080` on the Docker host and forward **all paths** for the hostname. Preserve Host and Authorization headers and support streaming and WebSocket upgrades. The bundled Caddy service routes inference to Go and the console to Node; it does not terminate public TLS. Go and Node have no published host ports.

If your reverse proxy runs elsewhere, configure the reachable private bind address and trusted proxy addresses as described in the [console deployment guide](../frontend/README.md#production-with-docker-compose). That guide also covers forwarded client IPs and alternative Docker networking.

After startup:

1. Open your HTTPS origin and sign in with the console password.
2. Choose **Connect Codex account** on Overview or Credentials and complete device approval. You can also import supported provider credential files through Credentials. See [account setup](../frontend/README.md#connect-a-codex-subscription) for device-authentication prerequisites.
3. Obtain a client API key from the console's API-key section or the generated configuration's `api-keys` list.
4. Use `https://gateway.example.com/v1` as an OpenAI-compatible client base URL, then follow [API usage](api-usage.md).

The public gateway deliberately returns 404 for `/v0/management` and `/management.html`. Administration goes through the signed-in console.

## Go server

Run from the repository root:

```sh
go build -o cli-proxy-api ./cmd/server
mkdir -p auths
openssl rand -hex 32
```

Create `config.yaml` with the following minimal configuration. Replace the client-key placeholder with the generated value before starting:

```yaml
host: "127.0.0.1"
port: 8317
auth-dir: "./auths"
api-keys:
  - "REPLACE_WITH_GENERATED_CLIENT_KEY"
ws-auth: true
remote-management:
  allow-remote: false
  secret-key: ""
  disable-control-panel: true
```

For the full set of options, use [config.example.yaml](../config.example.yaml). Its sample client keys must also be replaced. Relative paths in this setup assume the repository root is the working directory.

### Add provider credentials

Choose one of these approaches:

**Account login:** run the device flow, complete approval in a browser, then start the server:

```sh
./cli-proxy-api --config config.yaml --codex-device-login
./cli-proxy-api --config config.yaml
```

The login command saves credentials under `auth-dir` and exits. Additional CLI login options are listed by `./cli-proxy-api --help`. Device approval works on a headless host without publishing an OAuth callback port.

**Upstream API key:** add an OpenAI-compatible provider to `config.yaml`, replacing the URL, provider key, and upstream model ID:

```yaml
openai-compatibility:
  - name: "my-provider"
    base-url: "https://provider.example/v1"
    api-key-entries:
      - api-key: "REPLACE_WITH_PROVIDER_API_KEY"
    models:
      - name: "UPSTREAM_MODEL_ID"
        alias: "my-model"
```

Then run `./cli-proxy-api --config config.yaml`. Clients request `my-model`; the gateway sends `UPSTREAM_MODEL_ID` upstream. Provider-specific configuration sections, such as `gemini-api-key`, `claude-api-key`, and `codex-api-key`, are documented in the configuration reference.

Check the local process with `curl --fail-with-body http://127.0.0.1:8317/healthz`, then [list models and send a request](api-usage.md). A successful health check confirms the server is running; an inference request verifies provider access.

For a persistent service, run the binary with a process manager using an explicit working directory and config path. Keep the listener private and put HTTPS in front of it for remote clients. The `tls` configuration can also load your own certificate and key.

## Go-only Docker Compose

The repository-root [docker-compose.yml](../docker-compose.yml) runs only the Go gateway. It is separate from the complete stack in `frontend/`.

Create `config.yaml` as above, but use `host: "0.0.0.0"` inside the container and `auth-dir: "/root/.cli-proxy-api"` to match its credential volume. Then:

```sh
mkdir -p auths logs plugins
docker compose build cli-proxy-api
docker compose run --rm --no-deps --pull never cli-proxy-api ./CLIProxyAPI --config config.yaml --codex-device-login
docker compose up -d --pull never cli-proxy-api
```

Skip the login command if you configured an upstream API key instead. The explicit build and `--pull never` use this checkout; the root Compose file otherwise defaults to pulling an upstream image.

Before deployment, edit the root Compose `ports` list for your intended exposure. For device login or API-key authentication behind a host reverse proxy, only `127.0.0.1:8317:8317` is needed. The supplied file publishes port 8317 and several OAuth callback ports on all host interfaces. Container `host: "127.0.0.1"` is not a substitute for restricting the published host port.

The root stack persists configuration, credentials, logs, and plugins in bind mounts. The `CLI_PROXY_CONFIG_PATH`, `CLI_PROXY_AUTH_PATH`, `CLI_PROXY_LOG_PATH`, and `CLI_PROXY_PLUGIN_PATH` variables can change their host paths. Create the config file before starting Docker so it is mounted as a file.

## Credentials and operations

| Credential | Purpose | Where it belongs |
| --- | --- | --- |
| Console password | Browser sign-in | Setup prompt; only its hash is stored in `AUTH_PASSWORD_HASH` |
| Management key | Administrative API access | `PRIVATE_MANAGEMENT_KEY` in Node; matching `MANAGEMENT_PASSWORD` or `remote-management.secret-key` in Go |
| Client API key | Inference API access | Go `api-keys`; caller's Authorization header |
| Provider credential | Upstream model access | Go `auth-dir` or provider configuration |

Keep at least one client API key configured: an empty `api-keys` list disables the built-in API-key authentication. Keep management access private. Standalone Go management routes are disabled when neither a management secret nor `MANAGEMENT_PASSWORD` is set.

Run Compose operations from the directory of the stack you deployed:

```sh
docker compose ps
docker compose logs --tail=100
docker compose down
```

Back up configuration and credentials before updates. For the console stack, preserve `frontend/.env`, `frontend/deploy/data/config/`, and the named auth, log, and plugin volumes. `docker compose down` preserves named volumes; `docker compose down --volumes` deletes them. For the root stack, preserve the configured bind-mount directories. Protect logs too: request logging can include prompts and responses.

To update the console stack after checking out the desired revision, run `docker compose up --build -d` from `frontend/`. For the Go-only stack, repeat its build and `up --pull never` commands. Console sessions are held in memory and end when Node restarts; saved provider credentials persist.
