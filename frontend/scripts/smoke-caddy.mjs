import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer, request as httpRequest } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

// Exercise the checked-in routing configuration with real Caddy and local mock
// upstreams. Session authentication itself is covered by smoke-server.mjs.
const configPath = fileURLToPath(new URL('../deploy/Caddyfile', import.meta.url));
const temporaryDirectory = await mkdtemp(join(tmpdir(), 'management-caddy-smoke-'));
const clientKey = 'smoke-client-key';
const calls = [];
const sockets = new Set();
const finishStreams = new Set();
const websocketPayload = Buffer.from('proxy-websocket-ok');
const websocketFrame = Buffer.concat([
  Buffer.from([0x81, websocketPayload.length]),
  websocketPayload
]);

function record(upstream, request, body = Buffer.alloc(0)) {
  const entry = {
    upstream,
    path: request.url,
    authorization: request.headers.authorization,
    cookie: request.headers.cookie,
    origin: request.headers.origin,
    forwardedFor: request.headers['x-forwarded-for'],
    bodyBytes: body.length,
    bodySHA256: createHash('sha256').update(body).digest('hex')
  };
  calls.push(entry);
  return entry;
}

function mockUpstream(name) {
  const server = createServer(async (request, response) => {
    try {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const entry = record(name, request, Buffer.concat(chunks));
      if (name === 'gateway' && entry.authorization !== `Bearer ${clientKey}`) {
        response.writeHead(401).end();
        return;
      }
      if (request.url === '/v1/responses?stream=1') {
        response.writeHead(200, { 'Content-Type': 'text/event-stream' });
        response.flushHeaders();
        response.write('data: first\n\n');
        const finish = () => {
          finishStreams.delete(finish);
          response.end('data: last\n\n');
        };
        finishStreams.add(finish);
        return;
      }
      response.writeHead(200, { 'Content-Type': 'application/json' });
      response.end(JSON.stringify(entry));
    } catch {
      response.destroy();
    }
  });
  server.on('connection', (socket) => {
    sockets.add(socket);
    socket.once('close', () => sockets.delete(socket));
  });
  server.on('upgrade', (request, socket) => {
    const entry = record(name, request);
    if (name !== 'gateway' || entry.authorization !== `Bearer ${clientKey}`) {
      socket.end('HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n');
      return;
    }
    const accept = createHash('sha1')
      .update(`${request.headers['sec-websocket-key']}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
      .digest('base64');
    socket.write(
      `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`
    );
    socket.write(websocketFrame);
  });
  return server;
}

async function listen(server) {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return server.address().port;
}

async function close(server) {
  if (server.listening) await new Promise((resolve) => server.close(resolve));
}

async function withCaddy(trustedProxies, test) {
  const reservation = createServer();
  const port = await listen(reservation);
  await close(reservation);
  const child = spawn(
    process.env.CADDY_BIN || 'caddy',
    ['run', '--config', configPath, '--adapter', 'caddyfile'],
    {
      env: {
        ...process.env,
        XDG_CONFIG_HOME: join(temporaryDirectory, 'config'),
        XDG_DATA_HOME: join(temporaryDirectory, 'data'),
        CADDY_LISTEN: `http://127.0.0.1:${port}`,
        CADDY_TRUSTED_PROXIES: trustedProxies,
        GATEWAY_UPSTREAM: `127.0.0.1:${gateway.address().port}`,
        MANAGEMENT_UPSTREAM: `127.0.0.1:${management.address().port}`
      },
      stdio: ['ignore', 'pipe', 'pipe']
    }
  );
  let output = '';
  try {
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => finish(new Error(`Caddy did not start: ${output}`)), 10_000);
      const failed = (error) =>
        finish(new Error(`Unable to start Caddy; install it or set CADDY_BIN. ${error.message}`));
      const exited = () => finish(new Error(`Caddy exited during startup: ${output}`));
      function finish(error) {
        clearTimeout(timeout);
        child.off('error', failed);
        child.off('exit', exited);
        if (error) reject(error);
        else resolve();
      }
      const collect = (chunk) => {
        output += chunk.toString();
        if (output.includes('serving initial configuration')) finish();
      };
      child.stdout.on('data', collect);
      child.stderr.on('data', collect);
      child.once('error', failed);
      child.once('exit', exited);
    });
    const base = `http://127.0.0.1:${port}`;
    const request = (path, options = {}) =>
      fetch(`${base}${path}`, {
        redirect: 'manual',
        signal: AbortSignal.timeout(10_000),
        ...options
      });
    await test({ base, port, request });
  } finally {
    if (child.pid && child.exitCode === null && child.signalCode === null) {
      const exited = once(child, 'exit');
      child.kill('SIGTERM');
      const kill = setTimeout(() => child.kill('SIGKILL'), 5_000);
      try {
        await exited;
      } finally {
        clearTimeout(kill);
      }
    }
  }
}

function websocket(port) {
  return new Promise((resolve, reject) => {
    let upgradedSocket;
    const request = httpRequest({
      hostname: '127.0.0.1',
      port,
      path: '/v1/responses?transport=websocket',
      headers: {
        Authorization: `Bearer ${clientKey}`,
        Connection: 'Upgrade',
        Upgrade: 'websocket',
        'Sec-WebSocket-Key': randomBytes(16).toString('base64'),
        'Sec-WebSocket-Version': '13'
      }
    });
    const timeout = setTimeout(() => {
      upgradedSocket?.destroy();
      request.destroy();
      reject(new Error('WebSocket proxy timed out'));
    }, 10_000);
    request.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    request.once('response', (response) => {
      clearTimeout(timeout);
      response.resume();
      reject(new Error(`Expected WebSocket upgrade, received HTTP ${response.statusCode}`));
    });
    request.once('upgrade', (response, socket, head) => {
      upgradedSocket = socket;
      let received = head;
      const check = () => {
        if (received.length < websocketFrame.length) return;
        clearTimeout(timeout);
        socket.destroy();
        try {
          assert.equal(response.statusCode, 101);
          assert.deepEqual(received.subarray(0, websocketFrame.length), websocketFrame);
          resolve();
        } catch (error) {
          reject(error);
        }
      };
      socket.on('data', (chunk) => {
        received = Buffer.concat([received, chunk]);
        check();
      });
      socket.once('error', (error) => {
        clearTimeout(timeout);
        reject(error);
      });
      check();
    });
    request.end();
  });
}

function rawRequest(port, path) {
  return new Promise((resolve, reject) => {
    const request = httpRequest({
      hostname: '127.0.0.1',
      port,
      path,
      headers: { Authorization: `Bearer ${clientKey}` }
    });
    const timeout = setTimeout(() => request.destroy(new Error('Proxy request timed out')), 10_000);
    request.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    request.once('response', (response) => {
      response.resume();
      response.once('end', () => {
        clearTimeout(timeout);
        resolve(response.statusCode);
      });
      response.once('error', (error) => {
        clearTimeout(timeout);
        reject(error);
      });
    });
    request.end();
  });
}

const gateway = mockUpstream('gateway');
const management = mockUpstream('management');
try {
  await listen(gateway);
  await listen(management);
  // Loopback is deliberately untrusted during this run: forged forwarded
  // addresses must not reach the Node login throttle or the Go gateway.
  await withCaddy('192.0.2.254/32', async ({ port, request }) => {
    const headers = { Authorization: `Bearer ${clientKey}`, 'X-Forwarded-For': '198.51.100.66' };
    for (const path of [
      '/v1',
      '/v1/models?limit=2',
      '/v1beta',
      '/v1beta/models/example:generateContent',
      '/openai/v1',
      '/openai/v1/videos',
      '/backend-api/codex',
      '/backend-api/codex/responses?stream=true'
    ]) {
      const response = await request(path, { headers });
      assert.equal(response.status, 200, path);
      const data = await response.json();
      assert.equal(data.upstream, 'gateway', path);
      assert.equal(data.path, path);
      assert.equal(data.authorization, `Bearer ${clientKey}`);
      assert.equal(data.forwardedFor, '127.0.0.1', 'Untrusted XFF must be overwritten');
    }
    assert.equal(
      (await request('/v1/models')).status,
      401,
      'Caddy must not invent client credentials'
    );
    for (const path of ['/', '/login', '/api/session', '/api/management/config', '/v1-unrelated']) {
      const response = await request(path, { headers });
      const data = await response.json();
      assert.equal(data.upstream, 'management', path);
      assert.equal(data.path, path);
      assert.equal(data.forwardedFor, '127.0.0.1');
    }
    const sessionResponse = await request('/api/session', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Origin: 'https://management.example.test',
        Cookie: '__Host-management_session=smoke-session'
      },
      body: JSON.stringify({ password: 'smoke-password' })
    });
    const sessionData = await sessionResponse.json();
    assert.equal(sessionData.upstream, 'management');
    assert.equal(sessionData.origin, 'https://management.example.test');
    assert.equal(sessionData.cookie, '__Host-management_session=smoke-session');
    for (const path of ['/v0', '/v0/management/config', '/management.html']) {
      const before = calls.length;
      assert.equal((await request(path, { headers })).status, 404, path);
      assert.equal(calls.length, before, 'Blocked routes must not reach either upstream');
    }
    for (const path of [
      '/v1/../v0/management/config',
      '/v1/%2e%2e/v0/management/config',
      '/v0%2fmanagement/config'
    ]) {
      const before = calls.length;
      assert.equal(await rawRequest(port, path), 404, path);
      assert.equal(calls.length, before, 'Path normalization must not bypass management isolation');
    }
    const payload = randomBytes(2 * 1024 * 1024 + 17);
    for (const path of ['/v1/images/edits', '/api/management/auth-files']) {
      const response = await request(path, { method: 'POST', headers, body: payload });
      const data = await response.json();
      assert.equal(data.bodyBytes, payload.length);
      assert.equal(data.bodySHA256, createHash('sha256').update(payload).digest('hex'));
    }
    const stream = await request('/v1/responses?stream=1', { headers });
    const reader = stream.body.getReader();
    let first = '';
    while (!first.includes('\n\n')) {
      const chunk = await reader.read();
      assert.equal(chunk.done, false, 'SSE ended before its first event');
      first += new TextDecoder().decode(chunk.value);
    }
    assert.equal(first, 'data: first\n\n', 'SSE must arrive before the upstream finishes');
    for (const finish of finishStreams) finish();
    let rest = '';
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      rest += new TextDecoder().decode(chunk.value);
    }
    assert.equal(rest, 'data: last\n\n');
    await websocket(port);
    assert.equal(calls.at(-1).path, '/v1/responses?transport=websocket');
    assert.equal(calls.at(-1).authorization, `Bearer ${clientKey}`);
  });
  await withCaddy('127.0.0.1/32 ::1/128', async ({ request }) => {
    for (const path of ['/api/session', '/v1/models']) {
      const response = await request(path, {
        headers: {
          Authorization: `Bearer ${clientKey}`,
          'X-Forwarded-For': '198.51.100.66, 203.0.113.20, 127.0.0.1'
        }
      });
      assert.equal(
        (await response.json()).forwardedFor,
        '203.0.113.20',
        'Strict trusted proxy traversal must ignore the spoofed leftmost address'
      );
    }
  });
  console.log(
    'Caddy smoke test passed: routing, management isolation, authorization, client IP, uploads, SSE, and WebSocket upgrade.'
  );
} finally {
  for (const finish of finishStreams) finish();
  for (const socket of sockets) socket.destroy();
  await Promise.all([close(gateway), close(management)]);
  await rm(temporaryDirectory, { recursive: true, force: true });
}
