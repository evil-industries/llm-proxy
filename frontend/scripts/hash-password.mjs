import { randomBytes, scrypt as scryptCallback } from 'node:crypto';
import { promisify } from 'node:util';
import { createInterface } from 'node:readline';
import { Writable } from 'node:stream';
import { SCRYPT_OPTIONS, SCRYPT_PREFIX } from '../src/lib/server/password-params.js';

const scrypt = promisify(scryptCallback);
let password;
if (process.stdin.isTTY) {
  process.stderr.write('Management password (input hidden): ');
  const silent = new Writable({
    write(_chunk, _encoding, callback) {
      callback();
    }
  });
  const readline = createInterface({ input: process.stdin, output: silent, terminal: true });
  password = await new Promise((resolve) => readline.question('', resolve));
  readline.close();
  process.stderr.write('\n');
} else {
  const chunks = [];
  let length = 0;
  for await (const chunk of process.stdin) {
    length += chunk.length;
    if (length > 4096) throw new Error('Password input is too long.');
    chunks.push(chunk);
  }
  password = Buffer.concat(chunks)
    .toString('utf8')
    .replace(/\r?\n$/, '');
}
if (typeof password !== 'string' || password.length < 12 || password.length > 1024) {
  process.stderr.write('Use a password between 12 and 1024 characters.\n');
  process.exitCode = 1;
} else {
  const salt = randomBytes(16);
  const hash = await scrypt(password, salt, 64, SCRYPT_OPTIONS);
  console.log(`${SCRYPT_PREFIX}$${salt.toString('hex')}$${hash.toString('hex')}`);
}
