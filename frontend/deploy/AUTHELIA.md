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

Set these values in the private environment file for the management container:

```dotenv
AUTH_MODE=oidc
ORIGIN=https://llm.rose.sh
OIDC_ISSUER=https://auth.rose.sh
OIDC_CLIENT_ID=llm-proxy-management
OIDC_CLIENT_SECRET='REPLACE_WITH_PRIVATE_CLIENT_SECRET'
OIDC_ALLOWED_USERS=julius
```

Keep `PRIVATE_MANAGEMENT_URL` and `PRIVATE_MANAGEMENT_KEY` at their existing values.
Remove `AUTH_PASSWORD_HASH` from this deployment's environment file.
The console rejects password sign-in in OIDC mode, even if an old hash remains configured.
Missing OIDC settings cause authentication to fail. They do not enable password authentication.

For alpha-one, the management environment file is `/opt/llm-proxy/console.env` on virtual machine (VM) 201.
The existing Compose service reads that file with `format: raw`.
In a raw environment file, write the client secret without surrounding quotes.
Build the management image from this change before you recreate the management container.
Use the existing release procedure to preserve the backend image, configuration, credentials, and volumes.

For the bundled Compose deployment, set these variables in `frontend/.env`.
The Compose file passes them to the management container.
Existing password deployments continue to use `AUTH_MODE=password` by default.

## Routing and verification

Keep the current Traefik route from `llm.rose.sh` to Caddy.
Caddy sends `/auth/oidc/*`, `/login`, and console requests to the management container.
Do not apply Authelia forward-auth to the complete hostname.
Inference routes retain their application programming interface (API) key authentication.
Direct `/v0/*` management routes remain blocked.
The management server never trusts `Remote-User` or other caller-supplied identity headers.

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
To revoke all console sessions after an access change, restart the management container.

Run one management instance. Sessions and pending sign-ins are held in process memory.
A restart cancels both. Multiple instances require a shared session store.
