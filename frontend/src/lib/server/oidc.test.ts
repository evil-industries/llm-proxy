import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { generateKeyPairSync, sign } from 'node:crypto';
import type { RequestEvent } from '@sveltejs/kit';
import { authStore, cookieOptions, sessionCookieName } from './auth';
import { loadConfiguration, ConfigurationError } from './config';
import { OIDCTransactions, OIDC_TRANSACTION_SECONDS, oidcCookieName } from './oidc';

const { environment } = vi.hoisted(() => ({ environment: {} as Record<string, string> }));
vi.mock('$env/dynamic/private', () => ({ env: environment }));
vi.mock('$app/environment', () => ({ dev: false }));
vi.mock('$lib/server/auth', () => import('./auth'));
vi.mock('$lib/server/config', () => import('./config'));
vi.mock('$lib/server/relay', () => import('./relay'));
vi.mock('$lib/server/oidc', () => import('./oidc'));
vi.mock('$lib/server/device-auth', () => ({
  deviceAuthSessions: { forget: vi.fn().mockResolvedValue(undefined) }
}));
import { POST as start } from '../../routes/auth/oidc/login/+server';
import { GET as callback } from '../../routes/auth/oidc/callback/+server';
import { POST as passwordLogin, DELETE as logout } from '../../routes/api/session/+server';
import { load as loginPage } from '../../routes/login/+page.server';
import { deviceAuthSessions } from '$lib/server/device-auth';
import { handle } from '../../hooks.server';

const issuer = 'https://auth.example';
const keys = generateKeyPairSync('rsa', { modulusLength: 2048 });
const otherKeys = generateKeyPairSync('rsa', { modulusLength: 2048 });
let sequence = 0;
let authorization: URL;
let claimOverrides: Record<string, unknown>;
let username: unknown;
let subject: string;
let badSignature: boolean;
let tokenCalls: number;
let eventSequence = 0;
const tokens: string[] = [];

function event(method: string, path: string, jar = new Map<string, string>()) {
  const url = new URL(path, 'https://llm.example');
  return {
    url,
    request: new Request(url, { method, headers: { origin: url.origin } }),
    cookies: {
      get: vi.fn((key: string) => jar.get(key)),
      set: vi.fn((key: string, value: string) => {
        jar.set(key, value);
        if (key === sessionCookieName(true)) tokens.push(value);
      }),
      delete: vi.fn((key: string) => {
        jar.delete(key);
      })
    },
    locals: {
      authenticated: false,
      managementConfiguration: loadConfiguration(environment, true),
      configurationError: null
    },
    getClientAddress: () => `oidc-${eventSequence++}`
  } as unknown as RequestEvent;
}

async function flow() {
  const jar = new Map<string, string>();
  const login = event('POST', '/auth/oidc/login', jar);
  const response = await start(login as Parameters<typeof start>[0]);
  expect(response.status).toBe(303);
  authorization = new URL(response.headers.get('location')!);
  const responseURL = `/auth/oidc/callback?code=authorization-code&state=${authorization.searchParams.get('state')}`;
  return { jar, login, response, responseURL, request: event('GET', responseURL, jar) };
}

beforeEach(() => {
  for (const key of Object.keys(environment)) delete environment[key];
  Object.assign(environment, {
    AUTH_MODE: 'oidc',
    ORIGIN: 'https://llm.example',
    OIDC_ISSUER: issuer,
    OIDC_CLIENT_ID: `console-${sequence++}`,
    OIDC_CLIENT_SECRET: 'private-client-secret',
    OIDC_ALLOWED_USERS: 'julius',
    PRIVATE_MANAGEMENT_URL: 'http://gateway:8317',
    PRIVATE_MANAGEMENT_KEY: 'private-backend-key'
  });
  claimOverrides = {};
  username = 'julius';
  subject = 'user-subject';
  badSignature = false;
  tokenCalls = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const request = new Request(input, init);
      const url = new URL(request.url);
      expect(url.origin).toBe(issuer);
      if (url.pathname === '/.well-known/openid-configuration') {
        return Response.json({
          issuer,
          authorization_endpoint: `${issuer}/authorize`,
          token_endpoint: `${issuer}/token`,
          userinfo_endpoint: `${issuer}/userinfo`,
          jwks_uri: `${issuer}/jwks`,
          response_types_supported: ['code'],
          subject_types_supported: ['public'],
          id_token_signing_alg_values_supported: ['RS256']
        });
      }
      if (url.pathname === '/jwks')
        return Response.json({
          keys: [
            { ...keys.publicKey.export({ format: 'jwk' }), kid: 'test', alg: 'RS256', use: 'sig' }
          ]
        });
      if (url.pathname === '/token') {
        tokenCalls++;
        expect(
          Buffer.from(request.headers.get('authorization')!.slice(6), 'base64')
            .toString()
            .split(':')
            .map(decodeURIComponent)
        ).toEqual([environment.OIDC_CLIENT_ID, 'private-client-secret']);
        const body = new URLSearchParams(await request.text());
        expect(body.get('redirect_uri')).toBe('https://llm.example/auth/oidc/callback');
        expect(body.get('grant_type')).toBe('authorization_code');
        const { calculatePKCECodeChallenge } = await import('openid-client');
        expect(await calculatePKCECodeChallenge(body.get('code_verifier')!)).toBe(
          authorization.searchParams.get('code_challenge')
        );
        const now = Math.floor(Date.now() / 1000);
        const claims = {
          iss: issuer,
          sub: 'user-subject',
          aud: environment.OIDC_CLIENT_ID,
          iat: now,
          exp: now + 300,
          nonce: authorization.searchParams.get('nonce'),
          ...claimOverrides
        };
        const payload = [{ alg: 'RS256', kid: 'test' }, claims]
          .map((value) => Buffer.from(JSON.stringify(value)).toString('base64url'))
          .join('.');
        const signature = sign(
          'RSA-SHA256',
          Buffer.from(payload),
          badSignature ? otherKeys.privateKey : keys.privateKey
        ).toString('base64url');
        return Response.json({
          access_token: 'private-access-token',
          token_type: 'Bearer',
          id_token: `${payload}.${signature}`
        });
      }
      if (url.pathname === '/userinfo') {
        expect(request.headers.get('authorization')).toBe('Bearer private-access-token');
        return Response.json({ sub: subject, preferred_username: username });
      }
      throw new Error(`Unexpected provider path: ${url.pathname}`);
    })
  );
});

afterEach(() => {
  for (const token of tokens.splice(0)) authStore.revoke(token);
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('Authelia configuration', () => {
  it('requires no password and fixes the callback to the configured origin', () => {
    const configuration = loadConfiguration(environment, true);
    expect(configuration.passwordHash).toBe('');
    expect(configuration.oidc?.redirectURI).toBe('https://llm.example/auth/oidc/callback');
  });
  it.each(['OIDC_ISSUER', 'OIDC_CLIENT_ID', 'OIDC_CLIENT_SECRET', 'OIDC_ALLOWED_USERS', 'ORIGIN'])(
    'fails closed without %s',
    (key) => {
      expect(() => loadConfiguration({ ...environment, [key]: '' }, true)).toThrow(
        ConfigurationError
      );
    }
  );
  it.each([
    'http://auth.example',
    'https://user:secret@auth.example',
    'https://auth.example?x=secret',
    'https://auth.example#fragment'
  ])('rejects an unsafe issuer: %s', (value) => {
    expect(() => loadConfiguration({ ...environment, OIDC_ISSUER: value })).toThrow(
      ConfigurationError
    );
  });
  it('rejects empty user lists and unknown authentication modes', () => {
    expect(() => loadConfiguration({ ...environment, OIDC_ALLOWED_USERS: ', ,' })).toThrow(
      ConfigurationError
    );
    expect(() => loadConfiguration({ ...environment, AUTH_MODE: 'typo' })).toThrow(
      ConfigurationError
    );
  });
});

describe('one-time login transactions', () => {
  it('binds transactions to the browser, configuration, and ten-minute lifetime', () => {
    let now = 0;
    const store = new OIDCTransactions(() => now);
    const configuration = loadConfiguration(environment).oidc!;
    const first = store.create(configuration)!;
    expect(store.consume('forged', configuration)).toBeUndefined();
    expect(store.consume(first.token, configuration)).toEqual(first.transaction);
    expect(store.consume(first.token, configuration)).toBeUndefined();
    const second = store.create(configuration)!;
    expect(store.consume(second.token, { ...configuration, clientId: 'other' })).toBeUndefined();
    const third = store.create(configuration)!;
    now = OIDC_TRANSACTION_SECONDS * 1000;
    expect(store.consume(third.token, configuration)).toBeUndefined();
  });
  it('limits pending transactions and releases expired entries', () => {
    let now = 0;
    const store = new OIDCTransactions(() => now);
    const configuration = loadConfiguration(environment).oidc!;
    for (let index = 0; index < 1000; index++) expect(store.create(configuration)).not.toBeNull();
    expect(store.create(configuration)).toBeNull();
    now = OIDC_TRANSACTION_SECONDS * 1000;
    expect(store.create(configuration)).not.toBeNull();
  });
});

describe('OpenID Connect sign-in with signed provider responses', () => {
  it('uses state, nonce, S256, a private cookie, and a fixed callback', async () => {
    const { login, response } = await flow();
    expect(authorization.origin).toBe(issuer);
    expect(Object.fromEntries(authorization.searchParams)).toMatchObject({
      client_id: environment.OIDC_CLIENT_ID,
      response_type: 'code',
      response_mode: 'query',
      redirect_uri: 'https://llm.example/auth/oidc/callback',
      scope: 'openid profile',
      code_challenge_method: 'S256'
    });
    expect(authorization.searchParams.get('state')!.length).toBeGreaterThanOrEqual(32);
    expect(authorization.searchParams.get('nonce')!.length).toBeGreaterThanOrEqual(32);
    expect(login.cookies.set).toHaveBeenCalledWith(oidcCookieName(true), expect.any(String), {
      ...cookieOptions(true),
      maxAge: 600
    });
    expect(response.headers.get('cache-control')).toBe('no-store');
    expect(response.headers.get('referrer-policy')).toBe('no-referrer');
    expect(response.headers.get('location')).not.toContain('private-');
  });
  it('creates a session, rotates the old session, and revokes it on logout', async () => {
    const { jar, request } = await flow();
    const old = authStore.createSession('old')!;
    jar.set(sessionCookieName(true), old);
    const oldIdentity = authStore.identity(old);
    const response = await callback(request as Parameters<typeof callback>[0]);
    expect(response.headers.get('location')).toBe('/');
    expect(authStore.authenticated(old)).toBe(false);
    expect(deviceAuthSessions.forget).toHaveBeenCalledWith(
      oldIdentity,
      request.locals.managementConfiguration
    );
    const token = jar.get(sessionCookieName(true));
    expect(authStore.authenticated(token)).toBe(true);
    expect(jar.has(oidcCookieName(true))).toBe(false);
    expect(request.cookies.set).toHaveBeenCalledWith(
      sessionCookieName(true),
      expect.any(String),
      cookieOptions(true)
    );
    const protectedEvent = event('GET', '/api/management/config', jar);
    const resolve = vi.fn().mockResolvedValue(new Response('authorized'));
    expect((await handle({ event: protectedEvent, resolve })).status).toBe(200);
    expect(protectedEvent.locals.authenticated).toBe(true);
    const exit = event('DELETE', '/api/session', jar);
    expect((await logout(exit as Parameters<typeof logout>[0])).status).toBe(200);
    expect(authStore.authenticated(token)).toBe(false);
  });
  it.each([
    'state',
    'nonce',
    'issuer',
    'audience',
    'expiry',
    'signature',
    'subject',
    'username',
    'missing username',
    'provider denial'
  ])('rejects an invalid %s without a session or provider details', async (failure) => {
    const { jar, request } = await flow();
    if (failure === 'state') request.url.searchParams.set('state', 'forged');
    if (failure === 'nonce') claimOverrides.nonce = 'forged';
    if (failure === 'issuer') claimOverrides.iss = 'https://attacker.example';
    if (failure === 'audience') claimOverrides.aud = 'different-client';
    if (failure === 'expiry') claimOverrides.exp = 1;
    if (failure === 'signature') badSignature = true;
    if (failure === 'subject') subject = 'different-user';
    if (failure === 'username') username = 'other-user';
    if (failure === 'missing username') username = undefined;
    if (failure === 'provider denial') {
      request.url.searchParams.delete('code');
      request.url.searchParams.set('error', 'access_denied');
      request.url.searchParams.set('error_description', 'private-provider-detail');
    }
    const response = await callback(request as Parameters<typeof callback>[0]);
    expect(response.headers.get('location')).toBe('/login?error=oidc');
    expect(jar.has(sessionCookieName(true))).toBe(false);
    expect(jar.has(oidcCookieName(true))).toBe(false);
    expect(await response.text()).not.toContain('private-');
    if (failure === 'state' || failure === 'provider denial') expect(tokenCalls).toBe(0);
  });
  it('rejects missing browser cookies and replayed callbacks before token exchange', async () => {
    const { request, responseURL } = await flow();
    const missing = event('GET', responseURL);
    expect(
      (await callback(missing as Parameters<typeof callback>[0])).headers.get('location')
    ).toBe('/login?error=oidc');
    expect(tokenCalls).toBe(0);
    const savedCookie = request.cookies.get(oidcCookieName(true))!;
    expect(
      (await callback(request as Parameters<typeof callback>[0])).headers.get('location')
    ).toBe('/');
    request.cookies.set(oidcCookieName(true), savedCookie, { path: '/' });
    expect(
      (await callback(request as Parameters<typeof callback>[0])).headers.get('location')
    ).toBe('/login?error=oidc');
    expect(tokenCalls).toBe(1);
  });
  it('cancels a pending sign-in when the user signs out', async () => {
    const { jar, responseURL } = await flow();
    const savedCookie = jar.get(oidcCookieName(true))!;
    await logout(event('DELETE', '/api/session', jar) as Parameters<typeof logout>[0]);
    jar.set(oidcCookieName(true), savedCookie);
    expect(
      (
        await callback(event('GET', responseURL, jar) as Parameters<typeof callback>[0])
      ).headers.get('location')
    ).toBe('/login?error=oidc');
    expect(tokenCalls).toBe(0);
  });
  it('does not accept a password or forged identity headers in OIDC mode', async () => {
    const login = event('POST', '/api/session');
    expect((await passwordLogin(login as Parameters<typeof passwordLogin>[0])).status).toBe(403);
    const request = event('GET', '/api/management/config');
    request.request.headers.set('Remote-User', 'julius');
    request.request.headers.set('Authorization', 'Bearer forged');
    expect((await handle({ event: request, resolve: vi.fn() })).status).toBe(401);
  });
  it('rejects cross-origin starts and limits requests before discovery', async () => {
    const request = event('POST', '/auth/oidc/login');
    request.request.headers.set('origin', 'https://attacker.example');
    expect((await start(request as Parameters<typeof start>[0])).status).toBe(403);
    request.request.headers.set('origin', request.url.origin);
    vi.spyOn(authStore, 'permitLogin').mockReturnValue(false);
    expect((await start(request as Parameters<typeof start>[0])).status).toBe(429);
    expect(fetch).not.toHaveBeenCalled();
  });
  it('returns a safe error after provider failure and retries discovery', async () => {
    vi.mocked(fetch).mockRejectedValueOnce(new Error('private-provider-detail'));
    const request = event('POST', '/auth/oidc/login');
    expect((await start(request as Parameters<typeof start>[0])).headers.get('location')).toBe(
      '/login?error=oidc'
    );
    expect(request.cookies.set).not.toHaveBeenCalled();
    await flow();
  });
  it('exposes only the sign-in mode and a fixed error to the login page', async () => {
    const request = event('GET', '/login?error=private-provider-detail');
    const data = await loginPage(request as Parameters<typeof loginPage>[0]);
    expect(data).toEqual({
      oidc: true,
      initialError:
        'Authelia sign-in failed or access was denied. Try again or contact your administrator.'
    });
  });
});
