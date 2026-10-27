# Authelia management authentication

The console supports OpenID Connect (OIDC) through Authelia.
For `llm.rose.sh`, use `AUTH_MODE=oidc` to disable console password authentication.
Authelia authenticates the user. The console permits only usernames in `OIDC_ALLOWED_USERS`.

## Configure Authelia

The existing Authelia instance must provide OIDC discovery at `https://auth.rose.sh/.well-known/openid-configuration`.
If OIDC is not enabled, first configure its signing keys and private provider settings.
See the [Authelia provider instructions](https://www.authelia.com/configuration/identity-providers/openid-connect/provider/).

1. Generate a private client secret and its digest with the Authelia command below.
2. Store the plaintext secret in the console's private `OIDC_CLIENT_SECRET` variable.
3. Insert the digest into [authelia.example.yaml](authelia.example.yaml).
4. Merge the example policy and client into the existing Authelia configuration.
5. Validate the complete configuration with the installed Authelia version.
6. Apply the configuration through the existing Authelia deployment procedure.

```sh
authelia crypto rand --length 64 --charset alphanumeric --hash pbkdf2
```

Keep both command outputs private.
Do not replace the existing provider configuration with the example fragment.
The example permits `julius` through the `llm_management` policy.
It requires an exact callback address and Proof Key for Code Exchange (PKCE) with S256.
The client uses `client_secret_basic` and RS256 identity tokens.
See the [Authelia client instructions](https://www.authelia.com/configuration/identity-providers/openid-connect/clients/).

Authelia OIDC authorization policies are separate from forward-auth access rules.
A rule for `factory.rose.sh` does not authorize this client.
The console also requires an exact username match from the verified UserInfo response.

## Configure llm.rose.sh

Set these runtime values on the **single Go service** (never during the asset build):

```dotenv
CONSOLE_ENABLED=true
CONSOLE_STATIC_DIR=/CLIProxyAPI/console
AUTH_MODE=oidc
ORIGIN=https://llm.rose.sh
OIDC_ISSUER=https://auth.rose.sh
OIDC_CLIENT_ID=llm-proxy-management
OIDC_CLIENT_SECRET=REPLACE_WITH_PRIVATE_CLIENT_SECRET
OIDC_ALLOWED_USERS=julius
```

Keep the existing confidential client registration and exact callback URL. Remove the old `PRIVATE_MANAGEMENT_URL` and `PRIVATE_MANAGEMENT_KEY` relay variables; Go no longer needs a console-to-backend key. `AUTH_PASSWORD_HASH` is unnecessary in OIDC mode. Missing OIDC settings fail closed and never enable password authentication.

See [STATIC-CONSOLE.md](STATIC-CONSOLE.md) before migrating existing environment files, config mounts and provider-account volumes. Preserve each environment file's parsing rules: Compose `format: raw` does not strip quotes, so existing raw files must contain unquoted values. Never source private environment files as shell scripts.

## Routing and verification

Route `llm.rose.sh` directly from Traefik to Go on container port 8317, preserving Host. Go serves the static console, `/auth/oidc/*`, `/login` and its authenticated API. No Caddy or Node service is required. Do not apply Authelia forward-auth to the complete hostname: inference retains its API-key authentication. Remote `/v0/*` and `/management.html` are blocked by Go with the console enabled. Caller-supplied identity headers are never trusted.

After deployment:

1. Open `https://llm.rose.sh/login` in a private browser session.
2. Select **Sign in with Authelia**.
3. Sign in as `julius` at `auth.rose.sh`.
4. Verify that the console opens after the callback.
5. Verify that an unlisted account cannot access the console.
6. Verify that `POST /api/session` rejects password authentication.
7. Verify that unauthenticated management requests return status 401.
8. Verify that inference requests still require their existing API key.
9. Sign out of the console.
10. Verify that the previous console session no longer permits management requests.

Automation that currently posts a console password to `/api/session` needs a separate update before this deployment changes authentication.
This includes alpha-one tools that manage client API keys through the public console.
Do not put a management key in a browser or expose the backend management port to replace this access.

## Session behavior

Sign-in uses a ten-minute, single-use transaction with a browser cookie, state, nonce, and PKCE.
The server validates the token signature, issuer, audience, expiry, nonce, and UserInfo subject before it authorizes the username.
Provider tokens and client secrets remain on the server and are not stored in the console session.
A successful sign-in creates the existing eight-hour console session and revokes the previous console session.

Console logout revokes the local session and cancels a pending sign-in from that browser.
It does not sign the user out of Authelia or other applications.
A later sign-in can reuse the active Authelia session.
Authelia account changes do not immediately revoke existing console sessions.
To revoke all console sessions after an access change, restart the Go container.

Run one management instance. Sessions and pending sign-ins are held in process memory.
A restart cancels both. Multiple instances require a shared session store.
