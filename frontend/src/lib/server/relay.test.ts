import { describe, expect, it, vi } from 'vitest';
import { relayManagement } from './relay';

const config = {
  passwordHash: 'unused',
  managementURL: 'http://private-proxy:8317/v0/management',
  managementKey: 'private-secret'
};
const request = (path: string, method = 'GET', body?: string, origin = 'https://console.example') =>
  new Request(`https://console.example/api/management/${path}`, {
    method,
    headers: {
      origin,
      'Content-Type': 'application/json',
      cookie: 'management_session=browser-secret',
      authorization: 'Bearer attacker-key'
    },
    body
  });
const upstream = () =>
  vi.fn<typeof fetch>().mockResolvedValue(
    new Response('{"debug":true}', {
      headers: { 'Set-Cookie': 'upstream=secret', 'X-Upstream-Secret': 'secret' }
    })
  );

describe('authenticated upstream relay', () => {
  it('uses only the configured endpoint and key, omitting browser credentials and upstream cookies', async () => {
    const fetcher = upstream();
    const response = await relayManagement(request('config'), 'config', config, fetcher);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ debug: true });
    const [url, init] = fetcher.mock.calls[0];
    expect(url).toBe('http://private-proxy:8317/v0/management/config');
    const headers = new Headers(init?.headers);
    expect(headers.get('authorization')).toBe('Bearer private-secret');
    expect(headers.get('cookie')).toBeNull();
    expect(init).toMatchObject({ redirect: 'error', credentials: 'omit', cache: 'no-store' });
    expect(response.headers.get('set-cookie')).toBeNull();
    expect(response.headers.get('x-upstream-secret')).toBeNull();
    expect(response.headers.get('cache-control')).toBe('no-store');
  });

  it.each([
    '../config',
    'https://attacker.example',
    'api-call',
    'config.yaml',
    'auth-files/download',
    'plugins',
    '__proto__',
    'config/'
  ])('rejects non-allowlisted path %s before contacting upstream', async (path) => {
    const fetcher = upstream();
    expect((await relayManagement(request('config'), path, config, fetcher)).status).toBe(404);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('rejects disallowed methods', async () => {
    const fetcher = upstream();
    expect(
      (await relayManagement(request('config', 'PUT', '{}'), 'config', config, fetcher)).status
    ).toBe(404);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it.each(['', 'null', 'https://attacker.example'])(
    'rejects mutation with invalid Origin %s',
    async (origin) => {
      const fetcher = upstream();
      expect(
        (
          await relayManagement(
            request('debug', 'PUT', '{"value":true}', origin),
            'debug',
            config,
            fetcher
          )
        ).status
      ).toBe(403);
      expect(fetcher).not.toHaveBeenCalled();
    }
  );

  it('forwards allowed JSON changes without altering body', async () => {
    const fetcher = upstream();
    expect(
      (await relayManagement(request('debug', 'PUT', '{"value":true}'), 'debug', config, fetcher))
        .status
    ).toBe(200);
    expect(await new Response(fetcher.mock.calls[0][1]?.body).text()).toBe('{"value":true}');
  });

  it.each([
    'auth-files?all=true',
    'auth-files?name=a&name=b',
    'api-keys?value=secret',
    'api-keys?index=-1'
  ])('rejects broad or unsupported delete query %s', async (path) => {
    const fetcher = upstream();
    expect(
      (await relayManagement(request(path, 'DELETE'), path.split('?')[0], config, fetcher)).status
    ).toBe(400);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('allows deletion by specific filename', async () => {
    const fetcher = upstream();
    expect(
      (
        await relayManagement(
          request('auth-files?name=example%26name.json', 'DELETE'),
          'auth-files',
          config,
          fetcher
        )
      ).status
    ).toBe(200);
    expect(fetcher.mock.calls[0][0]).toBe(
      'http://private-proxy:8317/v0/management/auth-files?name=example%26name.json'
    );
  });

  it('streams JSON larger than the former cap without buffering before fetch', async () => {
    const payload = JSON.stringify({ value: 'x'.repeat(2 * 1024 * 1024 + 1) });
    const incoming = request('debug', 'PUT', payload);
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (_url, init) => {
      expect(init?.body).toBe(incoming.body);
      expect(await new Response(init?.body).text()).toBe(payload);
      return new Response('{}');
    });
    expect((await relayManagement(incoming, 'debug', config, fetcher)).status).toBe(200);
  });

  it('streams multipart files above 5 MiB including their envelope', async () => {
    const file = JSON.stringify({ token: 'x'.repeat(6 * 1024 * 1024) });
    const form = new FormData();
    form.set('file', new Blob([file], { type: 'application/json' }), 'credential.json');
    const incoming = new Request('https://console.example/api/management/auth-files', {
      method: 'POST',
      headers: { origin: 'https://console.example' },
      body: form
    });
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (_url, init) => {
      const received = await new Response(init?.body, { headers: init?.headers }).formData();
      expect(await (received.get('file') as File).text()).toBe(file);
      return new Response('{}');
    });
    expect((await relayManagement(incoming, 'auth-files', config, fetcher)).status).toBe(200);
  });

  it('accepts JSON responses larger than the former 10 MiB cap', async () => {
    const payload = { entries: 'x'.repeat(10 * 1024 * 1024 + 1) };
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(payload)));
    const response = await relayManagement(request('logs'), 'logs', config, fetcher);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual(payload);
  });

  it('forwards identity mutations in the body without key material in URLs', async () => {
    const fetcher = upstream();
    const payload = JSON.stringify({ action: 'remove', value: 'private-key' });
    const response = await relayManagement(
      request('api-keys/mutate', 'POST', payload),
      'api-keys/mutate',
      config,
      fetcher
    );
    expect(response.status).toBe(200);
    expect(fetcher.mock.calls[0][0]).toBe(
      'http://private-proxy:8317/v0/management/api-keys/mutate'
    );
    expect(await new Response(fetcher.mock.calls[0][1]?.body).text()).toBe(payload);
  });

  it.each([
    ['notifications', 'GET', undefined],
    ['notifications', 'PUT', JSON.stringify({ enabled: true, token: 'ntfy-secret' })],
    ['notifications/test', 'POST', '{}']
  ])('relays the authenticated notification endpoint %s %s', async (path, method, body) => {
    const fetcher = upstream();
    const response = await relayManagement(request(path!, method!, body), path!, config, fetcher);
    expect(response.status).toBe(200);
    expect(fetcher.mock.calls[0][0]).toBe(`http://private-proxy:8317/v0/management/${path}`);
    if (body) expect(await new Response(fetcher.mock.calls[0][1]?.body).text()).toBe(body);
  });

  it('rejects cross-origin notification tests before publishing', async () => {
    const fetcher = upstream();
    const response = await relayManagement(
      request('notifications/test', 'POST', '{}', 'https://attacker.example'),
      'notifications/test',
      config,
      fetcher
    );
    expect(response.status).toBe(403);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it.each([401, 403, 500])('sanitizes upstream failure %i', async (status) => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response('private-secret', { status }));
    const response = await relayManagement(request('config'), 'config', config, fetcher);
    expect(response.status).toBe(502);
    expect(await response.text()).not.toContain('private-secret');
  });

  it('sanitizes network and invalid-JSON failures', async () => {
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(new Error('secret URL and token'));
    const response = await relayManagement(request('config'), 'config', config, fetcher);
    expect(response.status).toBe(502);
    expect(await response.text()).not.toContain('token');
    fetcher.mockResolvedValue(new Response('<html>private-secret</html>'));
    expect((await relayManagement(request('config'), 'config', config, fetcher)).status).toBe(502);
  });
});
