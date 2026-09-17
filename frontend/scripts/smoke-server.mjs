import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { randomBytes, scryptSync } from 'node:crypto';
import { SCRYPT_OPTIONS } from '../src/lib/server/password-params.js';

// Exercise the built adapter, hooks, cookies, and relay together, not just mocks of handlers.
const password = randomBytes(24).toString('base64url');
const salt = randomBytes(16);
const hash = `scrypt$131072$8$1$${salt.toString('hex')}$${scryptSync(password, salt, 64, SCRYPT_OPTIONS).toString('hex')}`;
const key = randomBytes(24).toString('hex');
const origin = 'https://management.example.test';
const requests = [];
const upstream = createServer(async (request, response) => {
  let bytes = 0;
  for await (const chunk of request) bytes += chunk.length;
  requests.push({
    url: request.url,
    bytes,
    authorization: request.headers.authorization,
    cookie: request.headers.cookie
  });
  response.setHeader('Content-Type', 'application/json');
  response.end(JSON.stringify({ debug: false, routing: { strategy: 'round-robin' } }));
});
upstream.listen(0, '127.0.0.1');
await once(upstream, 'listening');
const reservation = createServer();
reservation.listen(0, '127.0.0.1');
await once(reservation, 'listening');
const port = reservation.address().port;
await new Promise((resolve) => reservation.close(resolve));
const server = spawn(process.execPath, ['build'], {
  cwd: new URL('../', import.meta.url),
  env: {
    ...process.env,
    NODE_ENV: '',
    BODY_SIZE_LIMIT: 'Infinity',
    HOST: '127.0.0.1',
    PORT: String(port),
    ORIGIN: origin,
    AUTH_PASSWORD_HASH: hash,
    PRIVATE_MANAGEMENT_KEY: key,
    PRIVATE_MANAGEMENT_URL: `http://127.0.0.1:${upstream.address().port}`
  },
  stdio: ['ignore', 'pipe', 'pipe']
});
const base = `http://127.0.0.1:${port}`;
const request = (path, options) => fetch(`${base}${path}`, { redirect: 'manual', ...options });
let startupOutput = '';
try {
  await new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error('Built server did not start within 20 seconds.')),
      20_000
    );
    const exit = () => {
      clearTimeout(timer);
      reject(new Error('Built server failed to start. Run npm run build first.'));
    };
    server.once('exit', exit);
    server.stdout.on('data', (chunk) => {
      startupOutput += chunk.toString();
      if (startupOutput.includes('Listening on')) {
        clearTimeout(timer);
        server.off('exit', exit);
        resolve();
      }
    });
    server.stderr.on('data', (chunk) => {
      startupOutput += chunk.toString();
    });
  });
  let response = await request('/');
  assert.equal(response.status, 303);
  assert.equal(response.headers.get('location'), '/login');
  assert.equal((await request('/login')).status, 200);
  assert.equal((await request('/api/management/config')).status, 401);
  assert.equal((await request('/v0/management/config')).status, 404);
  assert.equal(requests.length, 0, 'Unauthenticated requests must never reach the upstream');

  response = await request('/api/session', {
    method: 'POST',
    headers: { Origin: origin, 'Content-Type': 'application/json' },
    body: JSON.stringify({ password })
  });
  assert.equal(response.status, 200);
  const setCookie = response.headers.get('set-cookie');
  assert.ok(setCookie?.includes('__Host-management_session='));
  assert.match(setCookie, /HttpOnly/i);
  assert.match(setCookie, /Secure/i, 'Built server must use secure cookies even without NODE_ENV');
  assert.match(setCookie, /SameSite=Lax/i);
  const cookie = setCookie.split(';')[0];
  response = await request('/api/management/config', { headers: { Cookie: cookie } });
  assert.equal(response.status, 200);
  assert.equal((await response.json()).debug, false);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].authorization, `Bearer ${key}`);
  assert.equal(requests[0].cookie, undefined);
  assert.equal(
    (
      await request('/api/management/debug', {
        method: 'PUT',
        headers: {
          Cookie: cookie,
          Origin: 'https://attacker.example',
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ value: true })
      })
    ).status,
    403
  );
  assert.equal(requests.length, 1, 'Cross-origin writes must never reach the upstream');
  const upload = new FormData();
  const file = JSON.stringify({ token: 'x'.repeat(6 * 1024 * 1024) });
  upload.set('file', new Blob([file], { type: 'application/json' }), 'credential.json');
  response = await request('/api/management/auth-files', {
    method: 'POST',
    headers: { Cookie: cookie, Origin: origin },
    body: upload
  });
  assert.equal(
    response.status,
    200,
    'Authenticated uploads must pass the Node adapter and relay without a size cap'
  );
  assert.equal(requests.length, 2);
  assert.ok(requests[1].bytes > file.length, 'The complete multipart upload must reach Go');

  assert.equal(
    (
      await request('/api/session', {
        method: 'DELETE',
        headers: { Cookie: cookie, Origin: origin }
      })
    ).status,
    200
  );
  assert.equal(
    (await request('/api/management/config', { headers: { Cookie: cookie } })).status,
    401
  );
  console.log(
    'Built server smoke test passed: auth gate, secure cookie, relay, uncapped upload, CSRF, and logout revocation.'
  );
} finally {
  server.kill('SIGTERM');
  if (server.exitCode === null) await once(server, 'exit');
  upstream.closeAllConnections();
  await new Promise((resolve) => upstream.close(resolve));
}
