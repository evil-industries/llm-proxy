import { describe, expect, it } from 'vitest';
import { scryptSync } from 'node:crypto';
import { SCRYPT_OPTIONS, SCRYPT_PREFIX } from './password-params.js';
import {
  AuthStore,
  SESSION_SECONDS,
  cookieOptions,
  passwordMatches,
  sameOrigin,
  sessionCookieName
} from './auth';
import { ConfigurationError, loadConfiguration } from './config';

const salt = '00112233445566778899aabbccddeeff';
const passwordHash = `${SCRYPT_PREFIX}$${salt}$${scryptSync('a-long-test-password', Buffer.from(salt, 'hex'), 64, SCRYPT_OPTIONS).toString('hex')}`;

describe('password authentication', () => {
  it('verifies the configured scrypt hash and rejects an incorrect password', async () => {
    expect(await passwordMatches('a-long-test-password', passwordHash)).toBe(true);
    expect(await passwordMatches('incorrect-password', passwordHash)).toBe(false);
    expect(await passwordMatches('a-long-test-password', 'malformed')).toBe(false);
    expect(await passwordMatches('a'.repeat(1025), passwordHash)).toBe(false);
  });

  it('sets secure host-only session cookies in production', () => {
    expect(sessionCookieName(true)).toBe('__Host-management_session');
    expect(cookieOptions(true)).toEqual({
      path: '/',
      httpOnly: true,
      secure: true,
      sameSite: 'lax',
      maxAge: 28800
    });
    expect(cookieOptions(false).secure).toBe(false);
  });
  it('bounds concurrent memory-hard password checks', async () => {
    const first = passwordMatches('a-long-test-password', passwordHash);
    const second = passwordMatches('a-long-test-password', passwordHash);
    expect(await passwordMatches('a-long-test-password', passwordHash)).toBeNull();
    expect(await first).toBe(true);
    expect(await second).toBe(true);
  });

  it('requires an exact same origin for unsafe requests', () => {
    const url = new URL('https://console.example/api/session');
    for (const origin of [
      undefined,
      'null',
      'https://evil.example',
      'http://console.example',
      'https://console.example.attacker.test'
    ]) {
      const request = new Request(url, { headers: origin ? { origin } : {} });
      expect(sameOrigin(request, url)).toBe(false);
    }
    expect(sameOrigin(new Request(url, { headers: { origin: url.origin } }), url)).toBe(true);
  });
});

describe('bounded in-memory sessions', () => {
  it('expires sessions deterministically and rejects tampered or revoked tokens', () => {
    let now = 100;
    const store = new AuthStore(() => now);
    const token = store.createSession('127.0.0.1')!;
    expect(store.authenticated(token)).toBe(true);
    expect(store.authenticated(`${token.slice(0, -1)}${token.endsWith('A') ? 'B' : 'A'}`)).toBe(
      false
    );
    expect(store.authenticated(undefined)).toBe(false);
    now += SESSION_SECONDS * 1000;
    expect(store.authenticated(token)).toBe(false);
    const next = store.createSession('127.0.0.1')!;
    store.revoke(next);
    expect(store.authenticated(next)).toBe(false);
  });

  it('limits failed login attempts per address and resets after the fixed window', () => {
    let now = 0;
    const store = new AuthStore(() => now);
    for (let index = 0; index < 5; index++) expect(store.permitLogin('one')).toBe(true);
    expect(store.permitLogin('one')).toBe(false);
    expect(store.permitLogin('two')).toBe(true);
    now = 15 * 60 * 1000;
    expect(store.permitLogin('one')).toBe(true);
  });

  it('does not grow the address or session maps without bound', () => {
    let now = 0;
    const store = new AuthStore(() => now);
    for (let index = 0; index < 1000; index++)
      expect(store.permitLogin(`address-${index}`)).toBe(true);
    expect(store.permitLogin('overflow')).toBe(false);
    for (let index = 0; index < 1000; index++)
      expect(store.createSession(`address-${index}`)).not.toBeNull();
    expect(store.createSession('overflow')).toBeNull();
    now = SESSION_SECONDS * 1000;
    expect(store.createSession('after-expiry')).not.toBeNull();
    expect(store.permitLogin('after-expiry')).toBe(true);
  });
});

describe('private server configuration', () => {
  const valid = {
    AUTH_PASSWORD_HASH: passwordHash,
    PRIVATE_MANAGEMENT_URL: 'http://proxy:8317',
    PRIVATE_MANAGEMENT_KEY: 'upstream-secret'
  };
  it('normalizes the fixed backend address', () => {
    expect(loadConfiguration(valid).managementURL).toBe('http://proxy:8317/v0/management');
    expect(
      loadConfiguration({ ...valid, PRIVATE_MANAGEMENT_URL: 'https://proxy/prefix/v0/management/' })
        .managementURL
    ).toBe('https://proxy/prefix/v0/management');
  });
  it.each(['AUTH_PASSWORD_HASH', 'PRIVATE_MANAGEMENT_URL', 'PRIVATE_MANAGEMENT_KEY'])(
    'fails closed when %s is missing',
    (key) => {
      expect(() => loadConfiguration({ ...valid, [key]: '' })).toThrow(ConfigurationError);
    }
  );
  it.each([
    undefined,
    'http://console.example',
    'https://console.example/subpath',
    'https://user@console.example',
    'https://console.example?x=1'
  ])('requires a public HTTPS origin in production: %s', (origin) => {
    expect(() => loadConfiguration({ ...valid, ORIGIN: origin }, true)).toThrow(ConfigurationError);
  });
  it('accepts an explicit HTTPS production origin', () => {
    expect(loadConfiguration({ ...valid, ORIGIN: 'https://console.example' }, true).origin).toBe(
      'https://console.example'
    );
  });
  it.each([
    'file:///etc/passwd',
    'http://secret@proxy',
    'https://proxy?secret=token',
    'https://proxy#fragment'
  ])('rejects unsafe addresses without exposing their contents', (address) => {
    try {
      loadConfiguration({ ...valid, PRIVATE_MANAGEMENT_URL: address });
      throw new Error('Expected rejection');
    } catch (error) {
      expect(error).toBeInstanceOf(ConfigurationError);
      expect(String(error)).not.toContain(address);
    }
  });
});
