import assert from 'node:assert/strict';
import { createServer as createHTTPServer } from 'node:http';
import { createServer as createHTTPSServer } from 'node:https';
import { spawn, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash, generateKeyPairSync, randomBytes, sign } from 'node:crypto';

// Use a local HTTPS provider to test the production adapter and OIDC library together.
const directory = await mkdtemp(join(tmpdir(), 'llm-oidc-'));
const origin = 'https://console.example.test';
const clientId = 'test-console';
const clientSecret = randomBytes(32).toString('hex');
const managementKey = randomBytes(32).toString('hex');
const signingKey = generateKeyPairSync('rsa', { modulusLength: 2048 });
const codes = new Map();
let username = 'julius';
let providerError;
let issuer;
let server;
let provider;
let upstream;
let requests = 0;
let output = '';

try {
  const certificate = join(directory, 'certificate.pem');
  const privateKey = join(directory, 'private.pem');
  const certificateConfig = join(directory, 'openssl.cnf');
  await writeFile(
    certificateConfig,
    [
      '[req]',
      'distinguished_name = dn',
      'x509_extensions = v3',
      'prompt = no',
      '[dn]',
      'CN = localhost',
      '[v3]',
      'subjectAltName = DNS:localhost,IP:127.0.0.1',
      'basicConstraints = critical,CA:TRUE',
      'keyUsage = critical,digitalSignature,keyEncipherment,keyCertSign',
      'extendedKeyUsage = serverAuth',
      ''
    ].join('\n'),
    { mode: 0o600 }
  );
  execFileSync(
    'openssl',
    [
      'req',
      '-x509',
      '-newkey',
      'rsa:2048',
      '-nodes',
      '-days',
      '1',
      '-keyout',
      privateKey,
      '-out',
      certificate,
      '-config',
      certificateConfig
    ],
    { stdio: 'ignore' }
  );
  provider = createHTTPSServer(
    { key: await readFile(privateKey), cert: await readFile(certificate) },
    async (request, response) => {
      try {
        const url = new URL(request.url, issuer);
        let result;
        if (url.pathname === '/.well-known/openid-configuration') {
          result = {
            issuer,
            authorization_endpoint: `${issuer}/authorize`,
            token_endpoint: `${issuer}/token`,
            userinfo_endpoint: `${issuer}/userinfo`,
            jwks_uri: `${issuer}/jwks`,
            response_types_supported: ['code'],
            subject_types_supported: ['public'],
            id_token_signing_alg_values_supported: ['RS256']
          };
        } else if (url.pathname === '/jwks') {
          result = {
            keys: [
              {
                ...signingKey.publicKey.export({ format: 'jwk' }),
                kid: 'test',
                alg: 'RS256',
                use: 'sig'
              }
            ]
          };
        } else if (url.pathname === '/token') {
          assert.deepEqual(
            Buffer.from(request.headers.authorization.slice(6), 'base64')
              .toString()
              .split(':')
              .map(decodeURIComponent),
            [clientId, clientSecret]
          );
          let body = '';
          for await (const chunk of request) body += chunk;
          const params = new URLSearchParams(body);
          const login = codes.get(params.get('code'));
          assert.ok(login);
          codes.delete(params.get('code'));
          assert.equal(params.get('redirect_uri'), `${origin}/auth/oidc/callback`);
          assert.equal(params.get('grant_type'), 'authorization_code');
          assert.equal(
            createHash('sha256').update(params.get('code_verifier')).digest('base64url'),
            login.get('code_challenge')
          );
          const now = Math.floor(Date.now() / 1000);
          const claims = {
            iss: issuer,
            sub: 'operator',
            aud: clientId,
            iat: now,
            exp: now + 300,
            nonce: login.get('nonce')
          };
          const payload = [{ alg: 'RS256', kid: 'test' }, claims]
            .map((value) => Buffer.from(JSON.stringify(value)).toString('base64url'))
            .join('.');
          const signature = sign(
            'RSA-SHA256',
            Buffer.from(payload),
            signingKey.privateKey
          ).toString('base64url');
          result = {
            token_type: 'Bearer',
            access_token: 'fixture-access-token',
            id_token: `${payload}.${signature}`
          };
        } else if (url.pathname === '/userinfo') {
          assert.equal(request.headers.authorization, 'Bearer fixture-access-token');
          result = { sub: 'operator', preferred_username: username };
        } else {
          throw new Error('Unexpected provider request.');
        }
        response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(result));
      } catch (error) {
        providerError = error;
        response.writeHead(500).end();
      }
    }
  );
  provider.listen(0, '127.0.0.1');
  await once(provider, 'listening');
  issuer = `https://127.0.0.1:${provider.address().port}`;
  upstream = createHTTPServer((request, response) => {
    requests++;
    assert.equal(request.headers.authorization, `Bearer ${managementKey}`);
    assert.equal(request.headers.cookie, undefined);
    response.writeHead(200, { 'Content-Type': 'application/json' }).end('{"debug":false}');
  });
  upstream.listen(0, '127.0.0.1');
  await once(upstream, 'listening');
  server = spawn(process.execPath, ['server.mjs'], {
    env: {
      ...process.env,
      NODE_ENV: 'production',
      NODE_EXTRA_CA_CERTS: certificate,
      HOST: '127.0.0.1',
      PORT: '0',
      ORIGIN: origin,
      AUTH_MODE: 'oidc',
      AUTH_PASSWORD_HASH: '',
      OIDC_ISSUER: issuer,
      OIDC_CLIENT_ID: clientId,
      OIDC_CLIENT_SECRET: clientSecret,
      OIDC_ALLOWED_USERS: 'julius',
      PRIVATE_MANAGEMENT_KEY: managementKey,
      PRIVATE_MANAGEMENT_URL: `http://127.0.0.1:${upstream.address().port}`
    },
    stdio: ['ignore', 'pipe', 'pipe']
  });
  await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error(`Server startup failed: ${output}`)), 15000);
    server.once('exit', (code) => {
      clearTimeout(timeout);
      reject(new Error(`Server exited: ${code}`));
    });
    server.stdout.on('data', (chunk) => {
      output += chunk;
      if (/Listening on http:\/\/127\.0\.0\.1:\d+/.test(output)) {
        clearTimeout(timeout);
        resolve();
      }
    });
    server.stderr.on('data', (chunk) => {
      output += chunk;
    });
  });
  const local = output.match(/Listening on (http:\/\/127\.0\.0\.1:\d+)/)[1];
  const request = (path, options = {}) =>
    fetch(`${local}${path}`, { ...options, redirect: 'manual' });
  const cookie = (response, name) =>
    response.headers
      .getSetCookie()
      .find((value) => value.startsWith(`${name}=`))
      ?.split(';')[0];
  let response = await request('/login');
  const page = await response.text();
  assert.match(page, /Sign in with Authelia/);
  assert.doesNotMatch(page, /type="password"/);
  assert.ok(!page.includes(clientSecret) && !page.includes(managementKey));
  assert.equal(
    (
      await request('/api/session', {
        method: 'POST',
        headers: { Origin: origin, 'Content-Type': 'application/json' },
        body: '{"password":"old-password"}'
      })
    ).status,
    403
  );
  assert.equal((await request('/api/management/config')).status, 401);
  assert.equal(requests, 0);
  assert.equal(
    (
      await request('/auth/oidc/login', {
        method: 'POST',
        headers: { Origin: 'https://attacker.example' }
      })
    ).status,
    403
  );
  async function begin() {
    const response = await request('/auth/oidc/login', {
      method: 'POST',
      headers: { Origin: origin }
    });
    assert.equal(response.status, 303);
    const url = new URL(response.headers.get('location'));
    assert.equal(url.origin, issuer);
    assert.equal(url.searchParams.get('redirect_uri'), `${origin}/auth/oidc/callback`);
    assert.equal(url.searchParams.get('code_challenge_method'), 'S256');
    const transaction = cookie(response, '__Host-management_oidc');
    assert.ok(transaction);
    const code = randomBytes(24).toString('hex');
    codes.set(code, url.searchParams);
    return {
      transaction,
      path: `/auth/oidc/callback?code=${code}&state=${url.searchParams.get('state')}`
    };
  }
  let login = await begin();
  response = await request(login.path, { headers: { Cookie: login.transaction } });
  if (providerError) throw providerError;
  assert.equal(response.status, 303);
  assert.equal(response.headers.get('location'), '/');
  assert.equal(response.headers.get('referrer-policy'), 'no-referrer');
  const session = cookie(response, '__Host-management_session');
  assert.ok(session);
  const attributes = response.headers
    .getSetCookie()
    .find((value) => value.startsWith('__Host-management_session='));
  for (const attribute of ['HttpOnly', 'Secure', 'SameSite=Lax', 'Path=/'])
    assert.ok(attributes.includes(attribute));
  response = await request('/api/management/config', { headers: { Cookie: session } });
  assert.equal(response.status, 200);
  assert.equal(requests, 1);
  assert.equal(
    (await request(login.path, { headers: { Cookie: login.transaction } })).headers.get('location'),
    '/login?error=oidc'
  );
  assert.equal(
    (
      await request('/api/session', {
        method: 'DELETE',
        headers: { Cookie: session, Origin: origin }
      })
    ).status,
    200
  );
  assert.equal(
    (await request('/api/management/config', { headers: { Cookie: session } })).status,
    401
  );
  username = 'not-permitted';
  login = await begin();
  response = await request(login.path, { headers: { Cookie: login.transaction } });
  if (providerError) throw providerError;
  assert.equal(response.headers.get('location'), '/login?error=oidc');
  assert.equal(cookie(response, '__Host-management_session'), undefined);
  console.log(
    'OIDC server test passed: HTTPS discovery, PKCE, signed tokens, access policy, sessions, password rejection, relay, and logout.'
  );
} finally {
  if (server && server.exitCode === null) {
    const exited = once(server, 'exit');
    server.kill('SIGTERM');
    await exited;
  }
  for (const listener of [provider, upstream]) {
    if (listener) {
      listener.closeAllConnections();
      await new Promise((resolve) => listener.close(resolve));
    }
  }
  await rm(directory, { recursive: true, force: true });
}
