import { execFile as execFileCallback } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import * as fs from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseEnv, promisify } from 'node:util';
import { setupCompose } from './setup-compose.mjs';

const execFile = promisify(execFileCallback);
const frontend = fileURLToPath(new URL('../', import.meta.url));
const origin = 'https://compose-smoke.example.test';
const imagePrefix = process.env.COMPOSE_TEST_IMAGE_PREFIX || 'gateway-review';
const project = `gateway-smoke-${process.pid}-${randomBytes(4).toString('hex')}`;
class SmokeError extends Error {}
const interrupted = new AbortController();
const interrupt = () => interrupted.abort();
process.once('SIGINT', interrupt);
process.once('SIGTERM', interrupt);
const ensure = (condition, message) => {
  if (!condition) throw new SmokeError(message);
};

// Requires Docker Compose and a host openssl executable. All certificates, tokens,
// accounts, and request logs belong to this temporary test stack and are removed.
// This fixture never opens outbound connections: it handles local inference and
// terminates only auth.openai.com CONNECT requests with the ephemeral trusted CA.
const upstreamSource = String.raw`
import { createServer } from 'node:http';
import { createServer as createSecureServer } from 'node:https';
import { readFileSync } from 'node:fs';
const pending = new Set();
let approved = false;
const state = { challenges: 0, pendingPolls: 0, tokenExchanges: 0, blockedConnects: 0 };
const sendJSON = (response, status, value) => {
  response.writeHead(status, { 'Content-Type': 'application/json' });
  response.end(JSON.stringify(value));
};
const readBody = async (request) => {
  let raw = '';
  for await (const data of request) {
    raw += data;
    if (raw.length > 1024 * 1024) throw new Error('Fixture body too large');
  }
  return raw;
};
const jwt = (kind) => {
  const encode = (value) => Buffer.from(JSON.stringify(value)).toString('base64url');
  return encode({ alg: 'HS256', typ: 'JWT' }) + '.' + encode({
    email: 'smoke-device@example.test', token_use: kind,
    iat: Math.floor(Date.now() / 1000), exp: Math.floor(Date.now() / 1000) + 3600,
    'https://api.openai.com/auth': { chatgpt_account_id: 'smoke-account', chatgpt_plan_type: 'plus' }
  }) + '.' + Buffer.from('smoke-only-signature').toString('base64url');
};
const secure = createSecureServer({
  key: readFileSync('/fixture/ca.key'), cert: readFileSync('/fixture/ca.crt'),
  ALPNProtocols: ['http/1.1']
}, async (request, response) => {
  try {
    if (request.method !== 'POST' || request.headers.host !== 'auth.openai.com') {
      sendJSON(response, 404, { error: 'Unsupported fixture request' }); return;
    }
    const raw = await readBody(request);
    if (request.url === '/api/accounts/deviceauth/usercode') {
      const body = JSON.parse(raw);
      if (!body.client_id) { sendJSON(response, 400, { error: 'Missing client' }); return; }
      approved = false; state.challenges++;
      sendJSON(response, 200, { device_auth_id: 'smoke-private-device-id', user_code: 'SMOKE-123', interval: 1, expires_in: 120 });
      return;
    }
    if (request.url === '/api/accounts/deviceauth/token') {
      const body = JSON.parse(raw);
      if (body.device_auth_id !== 'smoke-private-device-id' || body.user_code !== 'SMOKE-123') {
        sendJSON(response, 400, { error: 'Wrong device challenge' }); return;
      }
      if (!approved) {
        state.pendingPolls++; sendJSON(response, 403, { error: 'authorization_pending' }); return;
      }
      sendJSON(response, 200, {
        authorization_code: 'smoke-private-auth-code', code_verifier: 'smoke-private-verifier', code_challenge: 'smoke-private-challenge'
      }); return;
    }
    if (request.url === '/oauth/token') {
      const body = new URLSearchParams(raw);
      if (!approved || body.get('grant_type') !== 'authorization_code' ||
          body.get('code') !== 'smoke-private-auth-code' || body.get('code_verifier') !== 'smoke-private-verifier' ||
          body.get('redirect_uri') !== 'https://auth.openai.com/deviceauth/callback') {
        sendJSON(response, 400, { error: 'Wrong token exchange' }); return;
      }
      state.tokenExchanges++;
      sendJSON(response, 200, { access_token: jwt('access'), id_token: jwt('id'), refresh_token: 'smoke-private-refresh-token', token_type: 'Bearer', expires_in: 3600 });
      return;
    }
    sendJSON(response, 404, { error: 'Unsupported fixture endpoint' });
  } catch { sendJSON(response, 400, { error: 'Invalid fixture request' }); }
});
secure.on('tlsClientError', () => {});
const chunk = (content, finish_reason = null) => 'data: ' + JSON.stringify({
  id: 'smoke-stream', object: 'chat.completion.chunk', created: 1, model: 'smoke-model',
  choices: [{ index: 0, delta: { content }, finish_reason }]
}) + '\n\n';
const server = createServer(async (request, response) => {
  try {
    const isAbsolute = /^https?:\/\//.test(request.url);
    const target = new URL(request.url, 'http://test-upstream:9000');
    // An absolute proxy URL is served locally only for this exact fixture origin.
    if (isAbsolute && target.origin !== 'http://test-upstream:9000') {
      response.writeHead(403); response.end(); return;
    }
    const loopback = ['127.0.0.1', '::1', '::ffff:127.0.0.1'].includes(request.socket.remoteAddress);
    if (!isAbsolute && loopback && target.pathname === '/fixture-status' && request.method === 'GET') {
      sendJSON(response, 200, state); return;
    }
    if (!isAbsolute && loopback && target.pathname === '/approve-device' && request.method === 'POST') {
      approved = true; response.end('ok'); return;
    }
    if (!isAbsolute && loopback && target.pathname === '/release-stream' && request.method === 'POST') {
      for (const stream of pending) { stream.end(chunk('stream', 'stop') + 'data: [DONE]\n\n'); }
      pending.clear(); response.end('ok'); return;
    }
    if (target.pathname !== '/v1/chat/completions' || request.method !== 'POST' ||
        request.headers.authorization !== 'Bearer smoke-provider-key') {
      response.writeHead(404); response.end(); return;
    }
    const body = JSON.parse(await readBody(request));
    if (body.stream) {
      response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
      response.write(chunk('smoke ')); pending.add(response);
      response.on('close', () => pending.delete(response)); return;
    }
    sendJSON(response, 200, { id: 'smoke-chat', object: 'chat.completion', created: 1, model: 'smoke-model',
      choices: [{ index: 0, message: { role: 'assistant', content: 'smoke response' }, finish_reason: 'stop' }],
      usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } });
  } catch { sendJSON(response, 400, { error: 'Invalid fixture request' }); }
});
server.on('connect', (request, socket, head) => {
  socket.on('error', () => {});
  if (request.url !== 'auth.openai.com:443') {
    state.blockedConnects++; socket.end('HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n'); return;
  }
  socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
  if (head.length) socket.unshift(head);
  secure.emit('connection', socket);
});
server.listen(9000, '0.0.0.0');
`;

async function waitFor(check, label, maximum = 60_000) {
  const end = Date.now() + maximum;
  while (Date.now() < end) {
    ensure(!interrupted.signal.aborted, 'The smoke test was interrupted.');
    try {
      if (await check()) return;
    } catch {
      /* Retry transient startup/restart failures. */
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new SmokeError(`${label} did not become ready within ${maximum / 1000} seconds.`);
}

let temporary;
let attemptedStart = false;
let compose;
let failed = false;
try {
  ensure(
    /^[a-z0-9][a-z0-9_.\/:\-]*$/i.test(imagePrefix),
    'COMPOSE_TEST_IMAGE_PREFIX is not a valid image prefix.'
  );
  temporary = await fs.mkdtemp(join(tmpdir(), 'gateway-compose-smoke-'));
  try {
    await execFile('openssl', ['version'], { signal: interrupted.signal, maxBuffer: 4096 });
  } catch {
    throw new SmokeError(
      'The Compose smoke test requires a host openssl executable for its temporary local TLS fixture.'
    );
  }
  const certificatePath = join(temporary, 'ca.crt');
  const privateKeyPath = join(temporary, 'ca.key');
  const certificateConfigPath = join(temporary, 'openssl.cnf');
  await fs.writeFile(
    certificateConfigPath,
    [
      '[req]',
      'distinguished_name = dn',
      'x509_extensions = v3',
      'prompt = no',
      '[dn]',
      'CN = auth.openai.com',
      '[v3]',
      'subjectAltName = DNS:auth.openai.com',
      'basicConstraints = critical,CA:TRUE',
      'keyUsage = critical,digitalSignature,keyEncipherment,keyCertSign',
      'extendedKeyUsage = serverAuth',
      ''
    ].join('\n'),
    { mode: 0o600 }
  );
  try {
    await execFile(
      'openssl',
      [
        'req',
        '-x509',
        '-newkey',
        'rsa:2048',
        '-sha256',
        '-nodes',
        '-days',
        '1',
        '-keyout',
        privateKeyPath,
        '-out',
        certificatePath,
        '-config',
        certificateConfigPath
      ],
      { signal: interrupted.signal, maxBuffer: 65536 }
    );
  } catch {
    throw new SmokeError(
      'Could not create the temporary TLS certificate for the local device-auth fixture.'
    );
  }
  // The parent temporary directory is private; readonly file mounts must be readable by the container's non-root user.
  await fs.chmod(privateKeyPath, 0o644);
  await fs.chmod(certificatePath, 0o644);
  const password = randomBytes(32).toString('base64url');
  const setup = await setupCompose({ directory: temporary, origin, password });
  const generated = parseEnv(await fs.readFile(setup.envPath, 'utf8'));
  const initialConfig = await fs.readFile(setup.configPath, 'utf8');
  const clientKey = initialConfig.match(/api-keys:\n  - "([^"]+)"/)?.[1];
  ensure(clientKey, 'Bootstrap did not create an inference client key.');
  await fs.appendFile(
    setup.configPath,
    [
      'proxy-url: "http://test-upstream:9000"',
      'openai-compatibility:',
      '  - name: "smoke"',
      '    base-url: "http://test-upstream:9000/v1"',
      '    api-key-entries:',
      '      - api-key: "smoke-provider-key"',
      '    models:',
      '      - name: "smoke-model"',
      '        alias: "smoke-model"',
      ''
    ].join('\n')
  );
  const fixturePath = join(temporary, 'upstream.mjs');
  await fs.writeFile(fixturePath, upstreamSource, { mode: 0o644 });
  const overridePath = join(temporary, 'compose.override.json');
  await fs.writeFile(
    overridePath,
    JSON.stringify({
      services: {
        management: { image: `${imagePrefix}-management` },
        'cli-proxy-api': {
          image: `${imagePrefix}-cli-proxy-api`,
          command: ['./CLIProxyAPI', '--config', '/etc/gateway/config.yaml', '--local-model'],
          depends_on: ['test-upstream'],
          environment: { SSL_CERT_FILE: '/fixture/ca.crt' },
          volumes: [
            { type: 'bind', source: dirname(setup.configPath), target: '/etc/gateway' },
            { type: 'bind', source: certificatePath, target: '/fixture/ca.crt', read_only: true }
          ]
        },
        'test-upstream': {
          image: `${imagePrefix}-management`,
          command: ['node', '/fixture/upstream.mjs'],
          volumes: [
            { type: 'bind', source: fixturePath, target: '/fixture/upstream.mjs', read_only: true },
            { type: 'bind', source: certificatePath, target: '/fixture/ca.crt', read_only: true },
            { type: 'bind', source: privateKeyPath, target: '/fixture/ca.key', read_only: true }
          ],
          security_opt: ['no-new-privileges:true'],
          cap_drop: ['ALL']
        }
      }
    }),
    { mode: 0o600 }
  );
  const env = {
    ...process.env,
    ...generated,
    GATEWAY_BIND_ADDRESS: '127.0.0.1',
    GATEWAY_PORT: '0',
    CADDY_TRUSTED_PROXIES: '127.0.0.1/32 ::1/128'
  };
  const args = [
    'compose',
    '--project-name',
    project,
    '--env-file',
    setup.envPath,
    '-f',
    join(frontend, 'compose.yaml'),
    '-f',
    overridePath
  ];
  compose = async (command, maximum = 120_000) => {
    try {
      const result = await execFile('docker', [...args, ...command], {
        cwd: frontend,
        env,
        timeout: maximum,
        signal: command[0] === 'down' ? undefined : interrupted.signal,
        maxBuffer: 4 * 1024 * 1024
      });
      return result.stdout;
    } catch {
      // Docker output can contain resolved environment variables. Never print it here.
      throw new SmokeError(
        `Docker Compose ${command[0]} failed. Check Docker availability, image builds, and registry connectivity${process.env.COMPOSE_TEST_SKIP_BUILD === '1' ? `; prebuilt ${imagePrefix} images are required when skipping the build` : ''}.`
      );
    }
  };
  const resolved = JSON.parse(await compose(['config', '--format', 'json']));
  ensure(resolved.name === project, 'Compose project isolation could not be verified.');
  ensure(
    resolved.services['cli-proxy-api'].volumes.find((volume) => volume.target === '/etc/gateway')
      ?.source === dirname(setup.configPath),
    'The smoke test must mount only its temporary Go configuration.'
  );
  ensure(
    resolved.services.management.image === `${imagePrefix}-management`,
    'Unexpected management image.'
  );
  ensure(
    resolved.services['cli-proxy-api'].image === `${imagePrefix}-cli-proxy-api`,
    'Unexpected Go image.'
  );
  if (process.env.COMPOSE_TEST_SKIP_BUILD !== '1') {
    process.stdout.write('Building the local smoke-test images…\n');
    await compose(['build', 'management', 'cli-proxy-api'], 20 * 60_000);
  }
  attemptedStart = true;
  process.stdout.write('Starting the isolated Compose smoke-test stack…\n');
  await compose(['up', '-d', '--no-build', '--pull', 'missing']);
  const published = (await compose(['port', 'caddy', '8080'])).trim();
  ensure(
    /^127\.0\.0\.1:\d+$/.test(published),
    'The test ingress must publish only on random loopback.'
  );
  const base = `http://${published}`;
  const request = async (path, options = {}) => {
    try {
      return await fetch(`${base}${path}`, {
        redirect: 'manual',
        signal: AbortSignal.any([AbortSignal.timeout(30_000), interrupted.signal]),
        ...options,
        headers: { Host: new URL(origin).host, ...options.headers }
      });
    } catch {
      throw new SmokeError(`HTTP request failed for smoke-test endpoint ${path}.`);
    }
  };
  const json = async (response) => {
    try {
      return await response.json();
    } catch {
      throw new SmokeError('The smoke-test endpoint returned invalid JSON.');
    }
  };
  const fixtureControl = async (path, method = 'POST') => {
    ensure(
      ['/fixture-status', '/approve-device', '/release-stream'].includes(path),
      'Unknown local fixture control.'
    );
    const output = await compose(
      [
        'exec',
        '-T',
        'test-upstream',
        'node',
        '--input-type=module',
        '-e',
        `const response = await fetch(${JSON.stringify(`http://127.0.0.1:9000${path}`)}, { method: ${JSON.stringify(method)} }); if (!response.ok) process.exit(1); ${path === '/fixture-status' ? 'process.stdout.write(JSON.stringify(await response.json()));' : ''}`
      ],
      20_000
    );
    return path === '/fixture-status' ? JSON.parse(output) : undefined;
  };
  const assertPublicDeviceResult = (result) => {
    const prohibited = new Set([
      'state',
      'device_auth_id',
      'code_verifier',
      'code_challenge',
      'authorization_code',
      'access_token',
      'refresh_token',
      'id_token'
    ]);
    const visit = (value) => {
      if (!value || typeof value !== 'object') return;
      for (const [key, child] of Object.entries(value)) {
        ensure(!prohibited.has(key), 'A private device-auth field escaped the console broker.');
        visit(child);
      }
    };
    visit(result);
    ensure(
      !JSON.stringify(result).includes('smoke-private-'),
      'A private fixture credential escaped the console broker.'
    );
  };
  await waitFor(async () => (await request('/login')).status === 200, 'Console');
  await waitFor(
    async () => (await request('/v1/models')).status === 401,
    'Inference authentication'
  );
  ensure(
    (await request('/api/management/config')).status === 401,
    'Anonymous management relay access must be rejected.'
  );
  ensure(
    (await request('/v0/management/config')).status === 404,
    'Caddy must hide direct management routes.'
  );
  ensure(
    (await request('/management.html')).status === 404,
    'Caddy must hide the legacy management page.'
  );
  const inferenceHeaders = {
    Authorization: `Bearer ${clientKey}`,
    'Content-Type': 'application/json'
  };
  await waitFor(async () => {
    const response = await request('/v1/models', { headers: inferenceHeaders });
    if (response.status !== 200) return false;
    return (await json(response)).data?.some((model) => model.id === 'smoke-model');
  }, 'Local test model');
  const login = await request('/api/session', {
    method: 'POST',
    headers: { Origin: origin, 'Content-Type': 'application/json' },
    body: JSON.stringify({ password })
  });
  ensure(login.status === 200, `Console login returned ${login.status}.`);
  const setCookie = login.headers.get('set-cookie') ?? '';
  ensure(
    setCookie.startsWith('__Host-management_session=') &&
      /;\s*Secure(?:;|$)/i.test(setCookie) &&
      /;\s*HttpOnly(?:;|$)/i.test(setCookie) &&
      /;\s*SameSite=Lax(?:;|$)/i.test(setCookie) &&
      /;\s*Path=\/(?:;|$)/i.test(setCookie),
    'Console login must issue a secure host-only session cookie.'
  );
  const cookie = setCookie.split(';')[0];
  const managementHeaders = { Cookie: cookie, Origin: origin, 'Content-Type': 'application/json' };
  ensure(
    (await request('/api/management/config', { headers: managementHeaders })).status === 200,
    'Authenticated management relay did not reach Go.'
  );
  ensure(
    (
      await request('/v0/management/config', {
        headers: { Authorization: `Bearer ${generated.PRIVATE_MANAGEMENT_KEY}` }
      })
    ).status === 404,
    'A management key must not bypass Caddy route restrictions.'
  );
  ensure(
    (
      await request('/api/management/routing/strategy', {
        method: 'PUT',
        headers: { ...managementHeaders, Origin: 'https://other.example.test' },
        body: JSON.stringify({ value: 'fill-first' })
      })
    ).status === 403,
    'Cross-origin management writes must be rejected.'
  );

  await waitFor(
    async () => Boolean(await fixtureControl('/fixture-status', 'GET')),
    'Local HTTPS device fixture'
  );
  ensure(
    (await request('/api/codex/device-auth')).status === 401,
    'Device login must require a console session.'
  );
  const deviceStart = await request('/api/codex/device-auth', {
    method: 'POST',
    headers: managementHeaders,
    body: '{}'
  });
  ensure(deviceStart.status === 200, `Device login start returned ${deviceStart.status}.`);
  const startedDevice = await json(deviceStart);
  assertPublicDeviceResult(startedDevice);
  ensure(
    startedDevice.status === 'pending' && startedDevice.user_code === 'SMOKE-123',
    'The real Go device flow did not return the local fixture challenge.'
  );
  ensure(
    startedDevice.verification_uri === 'https://auth.openai.com/codex/device',
    'Device login returned an unexpected verification URL.'
  );
  const resumeDevice = await request('/api/codex/device-auth', { headers: managementHeaders });
  ensure(resumeDevice.status === 200, 'The device login attempt could not be resumed.');
  const resumedDevice = await json(resumeDevice);
  assertPublicDeviceResult(resumedDevice);
  ensure(
    resumedDevice.status === 'pending' && resumedDevice.user_code === startedDevice.user_code,
    'Resuming device login must retain the same pending challenge.'
  );
  await waitFor(
    async () => (await fixtureControl('/fixture-status', 'GET')).pendingPolls >= 1,
    'Pending device approval poll',
    15_000
  );
  ensure(
    (await fixtureControl('/fixture-status', 'GET')).challenges === 1,
    'Resuming device login unexpectedly created a second challenge.'
  );
  await fixtureControl('/approve-device');
  let completedDevice;
  await waitFor(
    async () => {
      const response = await request('/api/codex/device-auth', { headers: managementHeaders });
      if (response.status !== 200) return false;
      const result = await json(response);
      assertPublicDeviceResult(result);
      if (result.status === 'complete') {
        completedDevice = result;
        return true;
      }
      ensure(result.status === 'pending', 'Device login failed after local fixture approval.');
      return false;
    },
    'Go device token exchange and account persistence',
    30_000
  );
  ensure(
    completedDevice.account?.email === 'smoke-device@example.test' &&
      completedDevice.account?.plan_type === 'plus',
    'Device completion did not return the fixture account metadata.'
  );
  const deviceFilename = completedDevice.account?.name;
  ensure(
    typeof deviceFilename === 'string' && deviceFilename.endsWith('.json'),
    'The device flow did not report a persisted auth filename.'
  );
  ensure(
    (await fixtureControl('/fixture-status', 'GET')).tokenExchanges === 1,
    'The official Go OAuth token exchange did not run exactly once.'
  );
  const accountPersisted = async () => {
    const response = await request('/api/management/auth-files', { headers: managementHeaders });
    if (response.status !== 200) return false;
    const body = await json(response);
    return (
      Array.isArray(body.files) &&
      body.files.some(
        (file) =>
          file.name === deviceFilename &&
          file.email === 'smoke-device@example.test' &&
          file.provider === 'codex'
      )
    );
  };
  await waitFor(accountPersisted, 'Device credential in Go auth storage');

  for (const setting of ['request-log', 'logging-to-file']) {
    const response = await request(`/api/management/${setting}`, {
      method: 'PUT',
      headers: managementHeaders,
      body: JSON.stringify({ value: true })
    });
    ensure(response.status === 200, `Enabling ${setting} returned ${response.status}.`);
  }
  await waitFor(async () => {
    const response = await request('/api/management/config', { headers: managementHeaders });
    if (response.status !== 200) return false;
    const config = await json(response);
    return config['request-log'] === true && config['logging-to-file'] === true;
  }, 'Request logging configuration');

  const completion = await request('/v1/chat/completions', {
    method: 'POST',
    headers: inferenceHeaders,
    body: JSON.stringify({
      model: 'smoke-model',
      messages: [{ role: 'user', content: 'Local smoke test — 世界 🚀' }]
    })
  });
  ensure(completion.status === 200, `Local inference returned ${completion.status}.`);
  ensure(
    (await json(completion)).choices?.[0]?.message?.content === 'smoke response',
    'Local inference response did not pass through both proxies.'
  );
  const streaming = await request('/v1/chat/completions', {
    method: 'POST',
    headers: inferenceHeaders,
    body: JSON.stringify({
      model: 'smoke-model',
      stream: true,
      messages: [{ role: 'user', content: 'Local stream smoke test' }]
    })
  });
  ensure(streaming.status === 200 && streaming.body, 'Local streaming inference did not start.');
  const decoder = new TextDecoder();
  let buffer = '';
  let content = '';
  let released = false;
  let done = false;
  for await (const bytes of streaming.body) {
    buffer += decoder.decode(bytes, { stream: true }).replace(/\r\n/g, '\n');
    let end;
    while ((end = buffer.indexOf('\n\n')) !== -1) {
      const event = buffer.slice(0, end);
      buffer = buffer.slice(end + 2);
      for (const line of event.split('\n')) {
        if (!line.startsWith('data:')) continue;
        const data = line.slice(5).trim();
        if (data === '[DONE]') {
          done = true;
          continue;
        }
        const parsed = JSON.parse(data);
        content += parsed.choices?.[0]?.delta?.content ?? '';
      }
    }
    if (!released && content === 'smoke ') {
      await fixtureControl('/release-stream');
      released = true;
    }
  }
  ensure(
    released && done && content === 'smoke stream',
    'SSE chunks must arrive progressively before the local provider finishes.'
  );

  ensure(
    (await request('/api/management/request-logs')).status === 401,
    'Request-log inspection must require a console session.'
  );
  let inspectedLog;
  await waitFor(async () => {
    const response = await request('/api/management/request-logs', { headers: managementHeaders });
    if (response.status !== 200) return false;
    const body = await json(response);
    // Select the earlier completed non-streaming request so a streaming logger
    // finishing its final flush cannot change the file during preview comparison.
    for (const file of body.files ?? []) {
      if (
        file.kind !== 'request' ||
        !file.name.startsWith('v1-chat-completions-') ||
        file.size <= 128
      )
        continue;
      const preview = await request(
        `/api/management/request-logs/${encodeURIComponent(file.name)}?limit=65536`,
        { headers: managementHeaders }
      );
      if (preview.status === 200 && (await json(preview)).text?.includes('Local smoke test')) {
        inspectedLog = file;
        break;
      }
    }
    return Boolean(inspectedLog);
  }, 'Generated inference request log');
  const logPath = `/api/management/request-logs/${encodeURIComponent(inspectedLog.name)}`;
  ensure(
    (await request(logPath)).status === 401 &&
      (await request(`${logPath}/download`)).status === 401,
    'Anonymous log previews and downloads must be rejected.'
  );
  let logOffset = 0;
  let logText = '';
  let logPages = 0;
  while (true) {
    const response = await request(`${logPath}?offset=${logOffset}&limit=128`, {
      headers: managementHeaders
    });
    ensure(response.status === 200, `Request-log preview returned ${response.status}.`);
    const page = await json(response);
    ensure(
      page.name === inspectedLog.name &&
        typeof page.text === 'string' &&
        page.next_offset > logOffset,
      'Request-log pagination did not advance correctly.'
    );
    logText += page.text;
    logOffset = page.next_offset;
    logPages++;
    if (!page.has_more) break;
    ensure(logPages < 1000, 'Generated request-log pagination did not finish.');
  }
  ensure(
    logPages > 1 &&
      logText.includes('smoke-model') &&
      logText.includes('世界 🚀') &&
      !logText.includes('\uFFFD'),
    'The generated request was not inspectable across multiple log pages.'
  );
  const download = await request(`${logPath}/download`, { headers: managementHeaders });
  ensure(
    download.status === 200 &&
      /attachment/i.test(download.headers.get('content-disposition') ?? ''),
    'Request-log download did not return a file attachment.'
  );
  ensure(
    (await download.text()) === logText,
    'The full request-log attachment differs from its paged preview.'
  );

  const mutation = await request('/api/management/routing/strategy', {
    method: 'PUT',
    headers: managementHeaders,
    body: JSON.stringify({ value: 'fill-first' })
  });
  ensure(mutation.status === 200, `Saving routing configuration returned ${mutation.status}.`);
  const persisted = await fs.readFile(setup.configPath, 'utf8');
  ensure(
    /(?:^|\n)routing:\s*\n[ \t]+strategy:\s*["']?fill-first["']?\s*(?:\n|$)/.test(persisted),
    'Routing changes did not persist to the mounted configuration file.'
  );
  await compose(['restart', 'cli-proxy-api']);
  await waitFor(async () => {
    const response = await request('/api/management/config', { headers: managementHeaders });
    return response.status === 200 && (await json(response)).routing?.strategy === 'fill-first';
  }, 'Go restart with persisted routing');
  await waitFor(accountPersisted, 'Persisted device credential after Go restart');
  ensure(
    (await request('/api/session', { method: 'DELETE', headers: managementHeaders })).status ===
      200,
    'Console logout failed.'
  );
  ensure(
    (await request('/api/management/config', { headers: { Cookie: cookie } })).status === 401,
    'A revoked session must not reach management.'
  );
  process.stdout.write(
    'Compose smoke checks passed: real Caddy/Node/Go authentication, private management, CSRF, local TLS device approval/token exchange, local inference/SSE, paged request logs and full downloads, persisted configuration and device credentials across restart, and logout.\n'
  );
} catch (error) {
  failed = true;
  process.stderr.write(
    `Compose smoke test failed: ${interrupted.signal.aborted ? 'Interrupted.' : error instanceof SmokeError ? error.message : 'An unexpected local configuration, filesystem, or fixture error occurred.'}\n`
  );
} finally {
  if (attemptedStart && compose) {
    try {
      await compose(['down', '--volumes', '--remove-orphans'], 90_000);
    } catch {
      failed = true;
      process.stderr.write(
        `Cleanup failed for isolated project ${project}; remove only that project's test containers and volumes.\n`
      );
    }
  }
  if (temporary) {
    try {
      await fs.rm(temporary, { recursive: true, force: true });
    } catch {
      failed = true;
      process.stderr.write('Could not remove the temporary smoke-test directory.\n');
    }
  }
  process.exitCode = failed ? 1 : 0;
  process.off('SIGINT', interrupt);
  process.off('SIGTERM', interrupt);
}
