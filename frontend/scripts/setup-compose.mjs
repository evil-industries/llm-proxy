import { randomBytes, scrypt as scryptCallback } from 'node:crypto';
import * as fs from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { Writable } from 'node:stream';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { SCRYPT_OPTIONS, SCRYPT_PREFIX } from '../src/lib/server/password-params.js';

const scrypt = promisify(scryptCallback);

export class SetupError extends Error {}

export function validateOrigin(value) {
  const input = typeof value === 'string' ? value.trim() : '';
  try {
    // Reject paths and even empty query/fragment delimiters before URL normalization.
    if (!/^https:\/\/[^\s/?#\\]+\/?$/i.test(input)) throw new Error();
    const url = new URL(input);
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      !url.hostname ||
      url.origin.includes("'")
    )
      throw new Error();
    return url.origin;
  } catch {
    throw new SetupError(
      'Use an HTTPS origin without credentials, a path, query, or fragment, such as https://gateway.example.com.'
    );
  }
}

export async function hashConsolePassword(password) {
  if (
    typeof password !== 'string' ||
    password.length < 12 ||
    password.length > 1024 ||
    /[\r\n]/.test(password)
  )
    throw new SetupError('Use a single-line password between 12 and 1024 characters.');
  const salt = randomBytes(16);
  const hash = await scrypt(password, salt, 64, SCRYPT_OPTIONS);
  return `${SCRYPT_PREFIX}$${salt.toString('hex')}$${hash.toString('hex')}`;
}

function pathsFor(directory) {
  const root = resolve(directory);
  return {
    root,
    envPath: join(root, '.env'),
    configPath: join(root, 'deploy/data/config/config.yaml')
  };
}

async function optionalStat(path, fileSystem) {
  try {
    return await fileSystem.lstat(path);
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
}

export async function assertFreshSetup(directory, fileSystem = fs) {
  const paths = pathsFor(directory);
  if (!(await fileSystem.lstat(paths.root)).isDirectory())
    throw new SetupError('The frontend working directory must be a directory.');
  for (const path of [paths.envPath, paths.configPath]) {
    if (await optionalStat(path, fileSystem))
      throw new SetupError(
        `Refusing to overwrite ${path}. Keep an existing development .env separate and choose a fresh deployment directory.`
      );
  }
  return paths;
}

function renderFiles(origin, passwordHash, managementKey, clientKey) {
  return {
    env: [
      `ORIGIN='${origin}'`,
      `AUTH_PASSWORD_HASH='${passwordHash}'`,
      `PRIVATE_MANAGEMENT_KEY='${managementKey}'`,
      "PRIVATE_MANAGEMENT_URL='http://cli-proxy-api:8317'",
      'BODY_SIZE_LIMIT=Infinity',
      ''
    ].join('\n'),
    config: [
      'host: "0.0.0.0"',
      'port: 8317',
      'ws-auth: true',
      'usage-statistics-enabled: true',
      'auth-dir: "/var/lib/cliproxy/auths"',
      'remote-management:',
      '  allow-remote: true',
      '  secret-key: ""',
      '  disable-control-panel: true',
      'api-keys:',
      `  - "${clientKey}"`,
      ''
    ].join('\n')
  };
}

export async function setupCompose({
  directory = process.cwd(),
  origin,
  password,
  fileSystem = fs
}) {
  const publicOrigin = validateOrigin(origin);
  const paths = await assertFreshSetup(directory, fileSystem);
  const passwordHash = await hashConsolePassword(password);
  const files = renderFiles(
    publicOrigin,
    passwordHash,
    randomBytes(32).toString('base64url'),
    `sk-${randomBytes(32).toString('base64url')}`
  );
  // Hashing yields to other processes; check again before creating any paths.
  await assertFreshSetup(directory, fileSystem);
  const createdDirectories = [];
  const createdFiles = [];
  try {
    for (const path of [
      join(paths.root, 'deploy'),
      join(paths.root, 'deploy/data'),
      dirname(paths.configPath)
    ]) {
      try {
        await fileSystem.mkdir(path, { mode: 0o700 });
        createdDirectories.push(path);
      } catch (error) {
        if (error.code !== 'EEXIST') throw error;
      }
      // Do not follow deployment-directory symlinks when placing secret files.
      if (!(await fileSystem.lstat(path)).isDirectory())
        throw new SetupError(
          'Deployment data paths must be directories, not files or symbolic links.'
        );
    }
    for (const [path, contents] of [
      [paths.envPath, files.env],
      [paths.configPath, files.config]
    ]) {
      const handle = await fileSystem.open(path, 'wx', 0o600);
      try {
        const identity = await handle.stat();
        createdFiles.push({ path, identity });
        await handle.writeFile(contents, 'utf8');
      } finally {
        await handle.close();
      }
    }
  } catch (error) {
    for (const { path, identity } of createdFiles.reverse()) {
      try {
        const current = await optionalStat(path, fileSystem);
        // Leave any replacement created by another process untouched.
        if (current?.dev === identity.dev && current?.ino === identity.ino)
          await fileSystem.unlink(path);
      } catch {
        /* Leave files we cannot safely remove for the operator to inspect. */
      }
    }
    for (const path of createdDirectories.reverse()) {
      try {
        await fileSystem.rmdir(path);
      } catch {
        /* Never remove a nonempty directory or another process's files. */
      }
    }
    if (error instanceof SetupError) throw error;
    throw new SetupError(
      'Could not create deployment files. Existing files were preserved; check permissions and inspect the setup paths before retrying.'
    );
  }
  return { origin: publicOrigin, envPath: paths.envPath, configPath: paths.configPath };
}

async function readPassword(input, errorOutput) {
  if (input.isTTY) {
    errorOutput.write('Console password (12–1024 characters, input hidden): ');
    const silent = new Writable({
      write(_chunk, _encoding, callback) {
        callback();
      }
    });
    const readline = createInterface({ input, output: silent, terminal: true });
    try {
      return await new Promise((resolvePassword, reject) => {
        readline.question('', resolvePassword);
        readline.once('close', () => reject(new SetupError('Password entry was cancelled.')));
      });
    } finally {
      readline.close();
      errorOutput.write('\n');
    }
  }
  const chunks = [];
  let bytes = 0;
  for await (const chunk of input) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    bytes += buffer.length;
    if (bytes > 4096) throw new SetupError('Password input is too long.');
    chunks.push(buffer);
  }
  return Buffer.concat(chunks)
    .toString('utf8')
    .replace(/\r?\n$/, '');
}

export async function runCLI({
  args = process.argv.slice(2),
  directory = process.cwd(),
  input = process.stdin,
  output = process.stdout,
  errorOutput = process.stderr
} = {}) {
  try {
    if (args.length !== 1)
      throw new SetupError('Usage: node scripts/setup-compose.mjs https://gateway.example.com');
    const origin = validateOrigin(args[0]);
    await assertFreshSetup(directory);
    const password = await readPassword(input, errorOutput);
    const result = await setupCompose({ directory, origin, password });
    output.write(
      `Created ${result.envPath}\nCreated ${result.configPath}\nYour inference client key is stored under api-keys in ${result.configPath}.\nKeep these files private. Review your deployment settings before starting Docker Compose.\n`
    );
    return 0;
  } catch (error) {
    errorOutput.write(
      `${error instanceof SetupError ? error.message : 'Setup failed. Check the working directory and file permissions.'}\n`
    );
    return 1;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = await runCLI();
}
