import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { RequestEvent } from '@sveltejs/kit';
import { authStore, sessionCookieName } from './auth';
import * as auth from './auth';
import { loadConfiguration } from './config';

const { environment } = vi.hoisted(() => ({ environment: {} as Record<string, string> }));
vi.mock('$env/dynamic/private', () => ({ env: environment }));
vi.mock('$app/environment', () => ({ dev: false }));
vi.mock('$lib/server/auth', () => import('./auth'));
vi.mock('$lib/server/config', () => import('./config'));
vi.mock('$lib/server/relay', () => import('./relay'));
vi.mock('$lib/server/device-auth', () => import('./device-auth'));
vi.mock('$lib/api/validation', () => import('../api/validation'));
import { deviceAuthSessions, DeviceAuthError } from './device-auth';
import { GET, POST, DELETE } from '../../routes/api/codex/device-auth/+server';
import { POST as login, DELETE as logout } from '../../routes/api/session/+server';
import { handle } from '../../hooks.server';

const secret = 'browser-controlled-secret-that-must-not-leak';
const tokens = new Set<string>();
let sequence = 0;
function event(
  method: string,
  options: { path?: string; authenticated?: boolean; origin?: string; body?: unknown } = {}
) {
  const url = new URL(`https://console.example${options.path ?? '/api/codex/device-auth'}`);
  const token =
    options.authenticated === false
      ? undefined
      : authStore.createSession(`device-test-${sequence++}`)!;
  if (token) tokens.add(token);
  const jar = new Map(token ? [[sessionCookieName(true), token]] : []);
  return {
    url,
    request: new Request(url, {
      method,
      headers: {
        'Content-Type': 'application/json',
        ...(options.origin === '' ? {} : { origin: options.origin ?? url.origin }),
        authorization: `Bearer ${secret}`
      },
      body: options.body === undefined ? undefined : JSON.stringify(options.body)
    }),
    cookies: {
      get: vi.fn((name: string) => jar.get(name)),
      set: vi.fn((name: string, value: string) => {
        jar.set(name, value);
        tokens.add(value);
      }),
      delete: vi.fn((name: string) => {
        jar.delete(name);
      })
    },
    locals: {
      authenticated: Boolean(token),
      sessionIdentity: authStore.identity(token),
      managementConfiguration: loadConfiguration(environment),
      configurationError: null
    },
    params: {},
    getClientAddress: () => `device-route-${sequence++}`
  } as unknown as RequestEvent;
}

beforeEach(() => {
  Object.assign(environment, {
    ORIGIN: 'https://console.example',
    AUTH_PASSWORD_HASH: `scrypt$131072$8$1$${'0'.repeat(32)}$${'0'.repeat(128)}`,
    PRIVATE_MANAGEMENT_URL: 'http://private-gateway:8317',
    PRIVATE_MANAGEMENT_KEY: 'private-management-key'
  });
  vi.spyOn(deviceAuthSessions, 'run').mockResolvedValue({ status: 'idle' });
  vi.spyOn(deviceAuthSessions, 'forget').mockResolvedValue();
});
afterEach(() => {
  vi.restoreAllMocks();
  for (const token of tokens) authStore.revoke(token);
  tokens.clear();
});

describe('device login route authorization', () => {
  it.each(['GET', 'POST', 'DELETE'] as const)(
    'rejects anonymous %s without calling the backend store',
    async (method) => {
      const request = event(method, { authenticated: false });
      const operation = { GET, POST, DELETE }[method];
      const response = await operation(request as Parameters<typeof GET>[0]);
      expect(response.status).toBe(401);
      expect(deviceAuthSessions.run).not.toHaveBeenCalled();
    }
  );

  it('requires a verified session identity even when the authenticated flag is true', async () => {
    const request = event('POST');
    request.locals.sessionIdentity = undefined;
    expect((await POST(request as Parameters<typeof POST>[0])).status).toBe(401);
    expect(deviceAuthSessions.run).not.toHaveBeenCalled();
  });

  it('fails closed when the gateway configuration is unavailable', async () => {
    const request = event('GET');
    request.locals.managementConfiguration = null;
    expect((await GET(request as Parameters<typeof GET>[0])).status).toBe(503);
    expect(deviceAuthSessions.run).not.toHaveBeenCalled();
  });

  it.each(['POST', 'DELETE'] as const)(
    'rejects cross-origin or missing-origin %s',
    async (method) => {
      for (const origin of ['', 'null', 'https://attacker.example']) {
        const request = event(method, { origin });
        const operation = { POST, DELETE }[method];
        expect((await operation(request as Parameters<typeof POST>[0])).status).toBe(403);
      }
      expect(deviceAuthSessions.run).not.toHaveBeenCalled();
    }
  );

  it.each(['GET', 'POST', 'DELETE'] as const)(
    'rejects caller-selected state and all extra query parameters for %s',
    async (method) => {
      const operation = { GET, POST, DELETE }[method];
      for (const query of [`state=${secret}`, 'unexpected=true']) {
        const request = event(method, { path: `/api/codex/device-auth?${query}` });
        const response = await operation(request as Parameters<typeof GET>[0]);
        expect(response.status).toBe(400);
        expect(await response.text()).not.toContain(secret);
      }
      expect(deviceAuthSessions.run).not.toHaveBeenCalled();
    }
  );

  it('takes ownership only from the verified session, ignoring caller-supplied body and authorization', async () => {
    const request = event('POST', {
      body: { state: secret, owner: secret, managementKey: secret, url: 'https://attacker.example' }
    });
    const response = await POST(request as Parameters<typeof POST>[0]);
    expect(response.status).toBe(200);
    expect(deviceAuthSessions.run).toHaveBeenCalledExactlyOnceWith(
      request.locals.sessionIdentity,
      'POST',
      request.locals.managementConfiguration
    );
    expect(request.locals.sessionIdentity).not.toBe(request.cookies.get(sessionCookieName(true)));
    expect(await response.json()).toEqual({ status: 'idle' });
    expect(response.headers.get('cache-control')).toBe('no-store');
  });

  it('passes distinct verified identities for different console sessions', async () => {
    const first = event('GET');
    const second = event('GET');
    await GET(first as Parameters<typeof GET>[0]);
    await GET(second as Parameters<typeof GET>[0]);
    const identities = vi.mocked(deviceAuthSessions.run).mock.calls.map(([owner]) => owner);
    expect(identities).toEqual([first.locals.sessionIdentity, second.locals.sessionIdentity]);
    expect(identities[0]).not.toBe(identities[1]);
  });

  it('returns safe typed errors and sanitizes unexpected failures', async () => {
    vi.mocked(deviceAuthSessions.run).mockRejectedValueOnce(
      new DeviceAuthError(502, 'Retry your existing login.')
    );
    let response = await GET(event('GET') as Parameters<typeof GET>[0]);
    expect(response.status).toBe(502);
    expect(await response.json()).toEqual({ error: 'Retry your existing login.' });
    vi.mocked(deviceAuthSessions.run).mockRejectedValueOnce(new Error(secret));
    response = await GET(event('GET') as Parameters<typeof GET>[0]);
    expect(response.status).toBe(502);
    expect(await response.text()).not.toContain(secret);
  });
});

describe('device approval ownership and console session lifecycle', () => {
  it('derives ownership in the real hook only while the cookie is valid', async () => {
    const request = event('GET');
    const token = request.cookies.get(sessionCookieName(true))!;
    const identity = request.locals.sessionIdentity;
    request.locals.sessionIdentity = 'caller-selected-owner';
    const resolve = vi.fn().mockImplementation(() => new Response('ok'));
    expect((await handle({ event: request, resolve })).status).toBe(200);
    expect(request.locals.sessionIdentity).toBe(identity);
    expect(identity).toMatch(/^[a-f0-9]{64}$/);
    expect(identity).not.toBe(token);
    authStore.revoke(token);
    expect((await handle({ event: request, resolve })).status).toBe(401);
    expect(request.locals.sessionIdentity).toBeUndefined();
    expect(resolve).toHaveBeenCalledTimes(1);
  });

  it('revokes access and begins cleanup on logout without waiting for gateway cancellation', async () => {
    vi.mocked(deviceAuthSessions.forget).mockImplementation(() => new Promise(() => {}));
    const request = event('DELETE', { path: '/api/session' });
    const token = request.cookies.get(sessionCookieName(true));
    const owner = request.locals.sessionIdentity;
    const response = await logout(request as Parameters<typeof logout>[0]);
    expect(response.status).toBe(200);
    expect(authStore.authenticated(token)).toBe(false);
    expect(deviceAuthSessions.forget).toHaveBeenCalledExactlyOnceWith(
      owner,
      request.locals.managementConfiguration
    );
    expect(request.cookies.delete).toHaveBeenCalledWith(sessionCookieName(true), { path: '/' });
    expect(await response.json()).toEqual({ authenticated: false });
  });

  it('does not revoke ownership when a cross-origin logout is rejected', async () => {
    const request = event('DELETE', { path: '/api/session', origin: 'https://attacker.example' });
    const token = request.cookies.get(sessionCookieName(true));
    expect((await logout(request as Parameters<typeof logout>[0])).status).toBe(403);
    expect(authStore.authenticated(token)).toBe(true);
    expect(deviceAuthSessions.forget).not.toHaveBeenCalled();
  });

  it('forgets the previous owner when a successful login rotates its session', async () => {
    vi.spyOn(auth, 'passwordMatches').mockResolvedValue(true);
    const request = event('POST', { path: '/api/session', body: { password: 'test-password' } });
    const previousToken = request.cookies.get(sessionCookieName(true));
    const previousOwner = request.locals.sessionIdentity;
    expect((await login(request as Parameters<typeof login>[0])).status).toBe(200);
    expect(deviceAuthSessions.forget).toHaveBeenCalledExactlyOnceWith(
      previousOwner,
      request.locals.managementConfiguration
    );
    expect(authStore.authenticated(previousToken)).toBe(false);
    const nextToken = request.cookies.get(sessionCookieName(true));
    expect(authStore.authenticated(nextToken)).toBe(true);
    expect(authStore.identity(nextToken)).not.toBe(previousOwner);
  });

  it('preserves the existing device approval on a rejected login', async () => {
    vi.spyOn(auth, 'passwordMatches').mockResolvedValue(false);
    const request = event('POST', { path: '/api/session', body: { password: 'incorrect' } });
    const token = request.cookies.get(sessionCookieName(true));
    expect((await login(request as Parameters<typeof login>[0])).status).toBe(401);
    expect(authStore.authenticated(token)).toBe(true);
    expect(deviceAuthSessions.forget).not.toHaveBeenCalled();
  });
});
