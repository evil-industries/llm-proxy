import { describe, expect, test, vi } from 'vitest';
import { createSessionManagementClient } from '../lib/api/client';

describe('cookie-authenticated management client', () => {
  test('uses only the same-origin relay, omitting backend authorization and caches', async () => {
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValue(Response.json({ 'api-keys': ['test-key'] }));
    const client = createSessionManagementClient(fetch);
    await expect(client.getAPIKeys()).resolves.toEqual(['test-key']);
    const [url, options] = fetch.mock.calls[0];
    expect(url).toBe('/api/management/api-keys');
    expect(options).toMatchObject({
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error'
    });
    expect(new Headers(options?.headers).has('Authorization')).toBe(false);
  });

  test('session expiry has a safe login error without upstream content', async () => {
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValue(Response.json({ error: 'backend-secret' }, { status: 401 }));
    await expect(createSessionManagementClient(fetch).getConfig()).rejects.toMatchObject({
      code: 'authentication',
      status: 401,
      message: 'Your session has expired. Sign in again.'
    });
  });

  test('mutations preserve JSON payloads while authenticating with the session cookie', async () => {
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValue(new Response(null, { status: 204 }));
    await createSessionManagementClient(fetch).setBoolean('debug', true);
    const [url, options] = fetch.mock.calls[0];
    expect(url).toBe('/api/management/debug');
    expect(options).toMatchObject({
      method: 'PUT',
      body: JSON.stringify({ value: true }),
      credentials: 'same-origin'
    });
    expect(new Headers(options?.headers).get('Content-Type')).toBe('application/json');
    expect(new Headers(options?.headers).has('Authorization')).toBe(false);
  });
});
