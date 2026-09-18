import test from 'node:test';
import assert from 'node:assert/strict';
import { scrypt as scryptCallback, timingSafeEqual } from 'node:crypto';
import * as fs from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Readable, Writable } from 'node:stream';
import { parseEnv, promisify } from 'node:util';
import { SCRYPT_OPTIONS } from '../src/lib/server/password-params.js';
import {
  assertFreshSetup,
  hashConsolePassword,
  runCLI,
  setupCompose,
  validateOrigin
} from './setup-compose.mjs';

const scrypt = promisify(scryptCallback);
const password = "pāssword with 'quotes' \" $ \\ 🦊";
async function directory(t) {
  const path = await fs.mkdtemp(join(tmpdir(), 'compose-setup-test-'));
  t.after(() => fs.rm(path, { recursive: true, force: true }));
  return path;
}
async function absent(path) {
  await assert.rejects(fs.lstat(path), { code: 'ENOENT' });
}
function outputCapture() {
  let text = '';
  return {
    stream: new Writable({
      write(chunk, _encoding, callback) {
        text += chunk;
        callback();
      }
    }),
    text: () => text
  };
}

test('accepts and canonicalizes only HTTPS origins', () => {
  assert.equal(validateOrigin(' https://GATEWAY.example.com:443/ '), 'https://gateway.example.com');
  assert.equal(validateOrigin('https://localhost:8443'), 'https://localhost:8443');
  for (const value of [
    'http://gateway.example.com',
    'https://user:secret@gateway.example.com',
    'https://gateway.example.com/path',
    'https://gateway.example.com/path/..',
    'https://gateway.example.com?x=1',
    'https://gateway.example.com?',
    'https://gateway.example.com#',
    'https://gateway.example.com\\path',
    'https://gate\nway.example.com',
    "https://foo'bar.example",
    'not-a-url',
    ''
  ]) {
    assert.throws(() => validateOrigin(value), /HTTPS origin/);
  }
});

test('writes private matching deployment configuration without storing the password', async (t) => {
  const root = await directory(t);
  const result = await setupCompose({
    directory: root,
    origin: 'https://GATEWAY.example.com/',
    password
  });
  const rawEnv = await fs.readFile(result.envPath, 'utf8');
  const env = parseEnv(rawEnv);
  const config = await fs.readFile(result.configPath, 'utf8');
  assert.deepEqual(Object.keys(result).sort(), ['configPath', 'envPath', 'origin']);
  assert.equal(env.ORIGIN, 'https://gateway.example.com');
  assert.equal(env.PRIVATE_MANAGEMENT_URL, 'http://cli-proxy-api:8317');
  assert.equal(env.BODY_SIZE_LIMIT, 'Infinity');
  assert.match(env.PRIVATE_MANAGEMENT_KEY, /^[A-Za-z0-9_-]{43}$/);
  assert.match(rawEnv, /AUTH_PASSWORD_HASH='scrypt\$131072\$8\$1\$/);
  assert.match(env.AUTH_PASSWORD_HASH, /^scrypt\$131072\$8\$1\$[a-f0-9]{32}\$[a-f0-9]{128}$/);
  const [, , , , salt, expected] = env.AUTH_PASSWORD_HASH.split('$');
  const actual = await scrypt(password, Buffer.from(salt, 'hex'), 64, SCRYPT_OPTIONS);
  assert.ok(
    timingSafeEqual(actual, Buffer.from(expected, 'hex')),
    'Unicode, quotes and dollar signs must retain their password bytes'
  );
  assert.ok(!rawEnv.includes(password));
  assert.ok(!config.includes(password));
  assert.match(
    config,
    /host: "0\.0\.0\.0"\nport: 8317\nws-auth: true\nusage-statistics-enabled: true\nauth-dir: "\/var\/lib\/cliproxy\/auths"/
  );
  assert.match(
    config,
    /remote-management:\n  allow-remote: true\n  secret-key: ""\n  disable-control-panel: true/
  );
  const clientKey = config.match(/api-keys:\n  - "(sk-[A-Za-z0-9_-]{43})"/)[1];
  assert.notEqual(clientKey.slice(3), env.PRIVATE_MANAGEMENT_KEY);
  assert.ok(
    !config.includes(env.PRIVATE_MANAGEMENT_KEY),
    'Go management key must come from the shared environment, not a second plaintext YAML copy'
  );
  assert.ok(!config.includes('notifications:'), 'default notification delivery must stay disabled');
  const compose = await fs.readFile(new URL('../compose.yaml', import.meta.url), 'utf8');
  assert.match(
    compose,
    /MANAGEMENT_PASSWORD:\s*\$\{PRIVATE_MANAGEMENT_KEY[:}]/,
    'Go must receive the same management key that the Node relay reads'
  );
  if (process.platform !== 'win32') {
    for (const path of [result.envPath, result.configPath])
      assert.equal((await fs.stat(path)).mode & 0o777, 0o600);
    for (const path of ['deploy', 'deploy/data', 'deploy/data/config'])
      assert.equal((await fs.stat(join(root, path))).mode & 0o777, 0o700);
  }
});

test('refuses either existing target before touching the other file', async (t) => {
  for (const target of ['.env', 'deploy/data/config/config.yaml']) {
    await t.test(target, async (t) => {
      const root = await directory(t);
      await fs.mkdir(join(root, 'deploy/data/config'), { recursive: true });
      await fs.writeFile(join(root, target), 'existing-operator-configuration');
      await assert.rejects(
        setupCompose({ directory: root, origin: 'https://gateway.example.com', password }),
        /Refusing to overwrite/
      );
      assert.equal(
        await fs.readFile(join(root, target), 'utf8'),
        'existing-operator-configuration'
      );
      await absent(join(root, target === '.env' ? 'deploy/data/config/config.yaml' : '.env'));
    });
  }
});

test('refuses dangling target links and data-directory symlinks without overwriting them', async (t) => {
  const root = await directory(t);
  await fs.symlink(join(root, 'missing-target'), join(root, '.env'));
  await assert.rejects(assertFreshSetup(root), /Refusing to overwrite/);
  await fs.unlink(join(root, '.env'));
  const external = await directory(t);
  await fs.mkdir(join(root, 'deploy'));
  await fs.symlink(external, join(root, 'deploy/data'));
  await assert.rejects(
    setupCompose({ directory: root, origin: 'https://gateway.example.com', password }),
    /symbolic links/
  );
  assert.deepEqual(await fs.readdir(external), []);
  await absent(join(root, '.env'));
  assert.ok((await fs.lstat(join(root, 'deploy/data'))).isSymbolicLink());
});

test('rolls back its files and empty directories after a partial write, preserving existing content', async (t) => {
  const root = await directory(t);
  await fs.mkdir(join(root, 'deploy'));
  await fs.writeFile(join(root, 'deploy/operator-note'), 'keep');
  const fileSystem = {
    ...fs,
    async open(path, flags, mode) {
      const handle = await fs.open(path, flags, mode);
      if (!path.endsWith('config.yaml')) return handle;
      return {
        stat: () => handle.stat(),
        close: () => handle.close(),
        async writeFile() {
          await handle.writeFile('partial');
          throw new Error('injected write failure');
        }
      };
    }
  };
  await assert.rejects(
    setupCompose({ directory: root, origin: 'https://gateway.example.com', password, fileSystem }),
    /Could not create deployment files/
  );
  await absent(join(root, '.env'));
  await absent(join(root, 'deploy/data'));
  assert.equal(await fs.readFile(join(root, 'deploy/operator-note'), 'utf8'), 'keep');
});

test('exclusive creation preserves a competing config and rolls back only this attempt', async (t) => {
  const root = await directory(t);
  const fileSystem = {
    ...fs,
    async open(path, flags, mode) {
      if (path.endsWith('config.yaml'))
        await fs.writeFile(path, 'another-process-config', { flag: 'wx' });
      return fs.open(path, flags, mode);
    }
  };
  await assert.rejects(
    setupCompose({ directory: root, origin: 'https://gateway.example.com', password, fileSystem }),
    /Could not create deployment files/
  );
  await absent(join(root, '.env'));
  assert.equal(
    await fs.readFile(join(root, 'deploy/data/config/config.yaml'), 'utf8'),
    'another-process-config'
  );
});

test('rejects unusable passwords before creating deployment data', async (t) => {
  for (const input of ['short', 'x'.repeat(1025), 'a-password-with\na-newline'])
    await assert.rejects(hashConsolePassword(input), /single-line password/);
  const root = await directory(t);
  await assert.rejects(
    setupCompose({ directory: root, origin: 'https://gateway.example.com', password: 'short' }),
    /single-line password/
  );
  assert.deepEqual(await fs.readdir(root), []);
});

test('CLI prints only file locations and never credentials', async (t) => {
  const root = await directory(t);
  const output = outputCapture();
  const errors = outputCapture();
  assert.equal(
    await runCLI({
      directory: root,
      args: ['https://gateway.example.com'],
      input: Readable.from([`${password}\n`]),
      output: output.stream,
      errorOutput: errors.stream
    }),
    0
  );
  const env = parseEnv(await fs.readFile(join(root, '.env'), 'utf8'));
  const config = await fs.readFile(join(root, 'deploy/data/config/config.yaml'), 'utf8');
  const clientKey = config.match(/api-keys:\n  - "([^"]+)"/)[1];
  const printed = output.text() + errors.text();
  for (const value of [password, env.AUTH_PASSWORD_HASH, env.PRIVATE_MANAGEMENT_KEY, clientKey])
    assert.ok(!printed.includes(value));
  assert.match(output.text(), /inference client key is stored under api-keys/);
  let reads = 0;
  const untouched = new Readable({
    read() {
      reads++;
      this.push(null);
    }
  });
  assert.equal(
    await runCLI({
      directory: root,
      args: ['https://gateway.example.com'],
      input: untouched,
      output: output.stream,
      errorOutput: errors.stream
    }),
    1
  );
  assert.equal(reads, 0, 'overwrite refusal must happen before password input');
});
