import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { scryptSync } from 'node:crypto';
import { SCRYPT_OPTIONS, SCRYPT_PREFIX } from './password-params.js';
import type { RequestEvent } from '@sveltejs/kit';
import { authStore, cookieOptions } from './auth';
import * as auth from './auth';
import { loadConfiguration } from './config';

const { environment } = vi.hoisted(() => ({ environment: {} as Record<string, string> }));
vi.mock('$env/dynamic/private', () => ({ env: environment }));
vi.mock('$app/environment', () => ({ dev: false }));
vi.mock('$lib/server/auth', () => import('./auth'));
vi.mock('$lib/server/config', () => import('./config'));
vi.mock('$lib/server/relay', () => import('./relay'));
vi.mock('$lib/server/device-auth', () => ({
  deviceAuthSessions: { forget: vi.fn().mockResolvedValue(undefined) }
}));
import { GET, POST, DELETE } from '../../routes/api/session/+server';
import { GET as managementGET } from '../../routes/api/management/[...path]/+server';
import { handle } from '../../hooks.server';

const salt = '00112233445566778899aabbccddeeff';
const passwordHash = `${SCRYPT_PREFIX}$${salt}$${scryptSync('a-long-test-password', Buffer.from(salt, 'hex'), 64, SCRYPT_OPTIONS).toString('hex')}`;
let address = 0;
function event(method: string, path: string, password?: string) {
  const jar = new Map<string, string>();
  const url = new URL(`https://console.example${path}`);
  const value = {
    url,
    request: new Request(url, {
      method,
      headers: { origin: url.origin, 'Content-Type': 'application/json' },
      body: password === undefined ? undefined : JSON.stringify({ password })
    }),
    cookies: {
      get: vi.fn((key: string) => jar.get(key)),
      set: vi.fn((key: string, value: string) => {
        jar.set(key, value);
      }),
      delete: vi.fn((key: string) => {
        jar.delete(key);
      })
    },
    locals: {
      authenticated: false,
      managementConfiguration: loadConfiguration(environment),
      configurationError: null
    },
    params: { path: 'config' },
    getClientAddress: () => `address-${address++}`
  };
  return value as unknown as RequestEvent;
}

beforeEach(() => {
  Object.assign(environment, {
    ORIGIN: 'https://console.example',
    AUTH_PASSWORD_HASH: passwordHash,
    PRIVATE_MANAGEMENT_URL: 'http://proxy:8317',
    PRIVATE_MANAGEMENT_KEY: 'never-public'
  });
});

afterEach(() => vi.restoreAllMocks());

describe('session endpoints and auth gate', () => {
  it('reports session status without configuration values', async () => {
    const response = await GET(event('GET', '/api/session') as Parameters<typeof GET>[0]);
    expect(await response.json()).toEqual({ authenticated: false, configured: true });
  });

  it('logs in with a secure cookie then revokes that session on logout', async () => {
    const login = event('POST', '/api/session', 'a-long-test-password');
    const response = await POST(login as Parameters<typeof POST>[0]);
    expect(response.status).toBe(200);
    expect(login.cookies.set).toHaveBeenCalledWith(
      '__Host-management_session',
      expect.any(String),
      cookieOptions(true)
    );
    const token = login.cookies.get('__Host-management_session');
    expect(authStore.authenticated(token)).toBe(true);
    login.request = new Request(login.url, {
      method: 'DELETE',
      headers: { origin: login.url.origin }
    });
    expect((await DELETE(login as Parameters<typeof DELETE>[0])).status).toBe(200);
    expect(authStore.authenticated(token)).toBe(false);
  });

  it('rejects incorrect passwords without issuing a cookie', async () => {
    const login = event('POST', '/api/session', 'incorrect');
    expect((await POST(login as Parameters<typeof POST>[0])).status).toBe(401);
    expect(login.cookies.set).not.toHaveBeenCalled();
  });

  it('identifies missing sign-in configuration separately from transient failures', async () => {
    const login = event('POST', '/api/session', 'a-long-test-password');
    login.locals.managementConfiguration = null;
    login.locals.configurationError = 'Missing configuration';
    const response = await POST(login as Parameters<typeof POST>[0]);
    expect(response.status).toBe(503);
    expect(await response.json()).toMatchObject({ code: 'not_configured' });
  });

  it.each(['busy', 'verification failure', 'session capacity'])(
    'marks %s as temporary and does not issue a cookie',
    async (scenario) => {
      const verify = vi.spyOn(auth, 'passwordMatches');
      if (scenario === 'busy') verify.mockResolvedValue(null);
      else if (scenario === 'verification failure')
        verify.mockRejectedValue(new Error('private detail'));
      else {
        verify.mockResolvedValue(true);
        vi.spyOn(authStore, 'createSession').mockReturnValue(null);
      }
      const login = event('POST', '/api/session', 'a-long-test-password');
      const response = await POST(login as Parameters<typeof POST>[0]);
      expect(response.status).toBe(503);
      expect(await response.json()).toMatchObject({ code: 'temporarily_unavailable' });
      expect(login.cookies.set).not.toHaveBeenCalled();
    }
  );

  it('rejects cross-origin login and logout', async () => {
    for (const method of ['POST', 'DELETE']) {
      const attempt = event(method, '/api/session');
      attempt.request = new Request(attempt.url, {
        method,
        headers: { origin: 'https://attacker.example' }
      });
      const operation = method === 'POST' ? POST : DELETE;
      expect((await operation(attempt as Parameters<typeof POST>[0])).status).toBe(403);
    }
  });

  it('rejects unauthenticated relay requests before any upstream call', async () => {
    const response = await managementGET(
      event('GET', '/api/management/config') as Parameters<typeof managementGET>[0]
    );
    expect(response.status).toBe(401);
  });

  it('redirects protected pages and rejects protected APIs for anonymous users', async () => {
    const resolve = vi.fn().mockResolvedValue(new Response('private'));
    await expect(handle({ event: event('GET', '/'), resolve })).rejects.toMatchObject({
      status: 303,
      location: '/login'
    });
    const response = await handle({ event: event('GET', '/api/management/config'), resolve });
    expect(response.status).toBe(401);
    expect(resolve).not.toHaveBeenCalled();
  });

  it('permits login pages and blocks direct management paths', async () => {
    const resolve = vi.fn().mockImplementation(() => new Response('login'));
    expect((await handle({ event: event('GET', '/login'), resolve })).status).toBe(200);
    expect((await handle({ event: event('GET', '/v0/management/config'), resolve })).status).toBe(
      404
    );
  });

  it('fails closed with a safe actionable message when configuration is missing', async () => {
    const requestEvent = event('GET', '/');
    delete environment.PRIVATE_MANAGEMENT_KEY;
    const response = await handle({ event: requestEvent, resolve: vi.fn() });
    expect(response.status).toBe(503);
    expect(await response.text()).toContain('PRIVATE_MANAGEMENT_KEY');
  });
});
