import { describe, expect, it, vi } from 'vitest';
import { createManagementClient, managementBaseURL, ManagementError } from './client';

function fixture(body: unknown = {}, status = 200) {
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' }
    })
  );
  const client = createManagementClient({
    baseUrl: 'http://localhost:8317',
    managementKey: 'management-secret',
    fetch: fetcher
  });
  return { client, fetcher };
}

describe('management addresses', () => {
  it.each([
    ['', '/v0/management'],
    [' https://proxy.example/ ', 'https://proxy.example/v0/management'],
    ['https://proxy.example/v0/management/', 'https://proxy.example/v0/management'],
    ['https://proxy.example/proxy', 'https://proxy.example/proxy/v0/management']
  ])('normalizes %s', (input, expected) => expect(managementBaseURL(input)).toBe(expected));

  it.each([
    'javascript:alert(1)',
    'file:///etc/passwd',
    'https://secret@example.com',
    'https://example.com?key=secret',
    'https://example.com#secret',
    'not a URL'
  ])('rejects unsafe or ambiguous addresses', (value) =>
    expect(() => managementBaseURL(value)).toThrow(ManagementError)
  );

  it('rejects a missing key before any request', () => {
    const fetcher = vi.fn<typeof fetch>();
    expect(() =>
      createManagementClient({
        baseUrl: '',
        managementKey: ' ',
        fetch: fetcher
      })
    ).toThrow(ManagementError);
    expect(fetcher).not.toHaveBeenCalled();
  });
});

describe('management transport', () => {
  it('authenticates without placing a key in the URL, cache, or cookies', async () => {
    const { client, fetcher } = fixture({ debug: true });
    expect(await client.getConfig()).toEqual({ debug: true });
    const [url, init] = fetcher.mock.calls[0];
    expect(url).toBe('http://localhost:8317/v0/management/config');
    expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer management-secret');
    expect(init).toMatchObject({
      cache: 'no-store',
      credentials: 'omit',
      redirect: 'error'
    });
  });

  it.each([
    [400, 'invalid-request'],
    [401, 'authentication'],
    [403, 'forbidden'],
    [404, 'not-found'],
    [429, 'rate-limit'],
    [500, 'server'],
    [503, 'server']
  ])('maps HTTP %i to a safe error', async (status, code) => {
    const { client } = fixture(
      {
        error: 'upstream leaked secret-token',
        message: 'Bearer management-secret'
      },
      status
    );
    try {
      await client.getConfig();
      throw new Error('Expected an HTTP error');
    } catch (error) {
      expect(error).toBeInstanceOf(ManagementError);
      expect(error).toMatchObject({ status, code });
      expect(String(error)).not.toMatch(/secret-token|management-secret/);
    }
  });

  it('does not leak transport error text', async () => {
    const { client, fetcher } = fixture();
    fetcher.mockRejectedValue(new TypeError('Fetch failed for https://host?secret-token'));
    await expect(client.getConfig()).rejects.toMatchObject({ code: 'network' });
    await expect(client.getConfig()).rejects.not.toThrow('secret-token');
  });

  it('reports a non-JSON success without exposing its body', async () => {
    const { client, fetcher } = fixture();
    fetcher.mockResolvedValue(new Response('<html>secret-token</html>'));
    await expect(client.getConfig()).rejects.toMatchObject({
      code: 'invalid-response'
    });
  });

  it('passes cancellation through without presenting it as a connection failure', async () => {
    const { client, fetcher } = fixture();
    const controller = new AbortController();
    const abort = new DOMException('The operation was aborted', 'AbortError');
    fetcher.mockRejectedValue(abort);
    controller.abort();
    await expect(client.getConfig(controller.signal)).rejects.toBe(abort);
    expect(fetcher.mock.calls[0][1]?.signal).toBe(controller.signal);
  });
});

describe('backend contracts', () => {
  it('normalizes null credential and key lists to empty lists', async () => {
    const { client, fetcher } = fixture({ files: null });
    expect(await client.getAuthFiles()).toEqual([]);
    fetcher.mockResolvedValue(new Response(JSON.stringify({ 'api-keys': null })));
    expect(await client.getAPIKeys()).toEqual([]);
  });

  it('includes credential identity when changing status', async () => {
    const { client, fetcher } = fixture({ status: 'ok' });
    await client.setAuthFileDisabled('account.json', true, 'index-2');
    expect(fetcher.mock.calls[0][0]).toMatch(/\/auth-files\/status$/);
    expect(fetcher.mock.calls[0][1]).toMatchObject({
      method: 'PATCH',
      body: JSON.stringify({
        name: 'account.json',
        disabled: true,
        auth_index: 'index-2'
      })
    });
  });

  it('uploads a single file with a browser-generated multipart boundary', async () => {
    const { client, fetcher } = fixture({ status: 'ok' });
    const file = new File(['{}'], 'account.json', { type: 'application/json' });
    await client.uploadAuthFile(file);
    const [, init] = fetcher.mock.calls[0];
    expect(init?.method).toBe('POST');
    expect(init?.body).toBeInstanceOf(FormData);
    expect((init?.body as FormData).get('file')).toBe(file);
    expect(new Headers(init?.headers).has('Content-Type')).toBe(false);
  });

  it('encodes filenames without changing query semantics', async () => {
    const { client, fetcher } = fixture({ status: 'ok' });
    await client.deleteAuthFile('name&all=true.json');
    const url = new URL(String(fetcher.mock.calls[0][0]));
    expect(url.searchParams.get('name')).toBe('name&all=true.json');
    expect(url.searchParams.has('all')).toBe(false);
  });

  it('sends the API key list in the backend array format, including an empty list', async () => {
    const { client, fetcher } = fixture({ status: 'ok' });
    await client.replaceAPIKeys([]);
    expect(fetcher.mock.calls[0][1]).toMatchObject({
      method: 'PUT',
      body: '[]'
    });
  });

  it('updates and removes keys by index without sending secrets in URLs', async () => {
    const { client, fetcher } = fixture({ status: 'ok' });
    await client.updateAPIKey(2, 'new-key');
    expect(fetcher.mock.calls[0][1]).toMatchObject({
      method: 'PATCH',
      body: JSON.stringify({ index: 2, value: 'new-key' })
    });
    fetcher.mockResolvedValue(new Response('{}'));
    await client.deleteAPIKey(2);
    expect(fetcher.mock.calls[1][0]).toMatch(/\/api-keys\?index=2$/);
    expect(fetcher.mock.calls[1][1]?.method).toBe('DELETE');
  });

  it('adds and removes API keys atomically by identity without secrets in the URL', async () => {
    const { client, fetcher } = fixture({ status: 'ok' });
    await client.addAPIKey('key-with&reserved=value');
    fetcher.mockResolvedValue(new Response('{}'));
    await client.removeAPIKey('key-with&reserved=value');
    expect(fetcher).toHaveBeenCalledTimes(2);
    for (const [index, action] of ['add', 'remove'].entries()) {
      const [url, options] = fetcher.mock.calls[index];
      expect(url).toBe('http://localhost:8317/v0/management/api-keys/mutate');
      expect(options).toMatchObject({
        method: 'POST',
        body: JSON.stringify({ action, value: 'key-with&reserved=value' })
      });
    }
  });

  it('uses value wrappers for settings and an enabled wrapper for plugins', async () => {
    const { client, fetcher } = fixture({ status: 'ok' });
    await client.setBoolean('debug', true);
    expect(fetcher.mock.calls[0][1]?.body).toBe('{"value":true}');
    fetcher.mockResolvedValue(new Response('{}'));
    await client.setRoutingStrategy('weighted-round-robin');
    expect(fetcher.mock.calls[1][1]?.body).toBe('{"value":"weighted-round-robin"}');
    fetcher.mockResolvedValue(new Response('{}'));
    await client.setPluginEnabled('provider&one', false);
    expect(fetcher.mock.calls[2][0]).toMatch(/\/plugins\/provider%26one\/enabled$/);
    expect(fetcher.mock.calls[2][1]?.body).toBe('{"enabled":false}');
  });

  it('loads usage from the non-consuming endpoint', async () => {
    const { client, fetcher } = fixture({ codex: {} });
    expect(await client.getAPIKeyUsage()).toEqual({ codex: {} });
    expect(fetcher.mock.calls[0][0]).toMatch(/\/api-key-usage$/);
  });

  it('encodes log cursors and retains zero timestamp', async () => {
    const { client, fetcher } = fixture({
      lines: [],
      'line-count': 0,
      'latest-timestamp': 0,
      'next-cursor': ''
    });
    await client.getLogs({ limit: 200, cursor: 'a+b/c=', after: 0 });
    const url = new URL(String(fetcher.mock.calls[0][0]));
    expect(Object.fromEntries(url.searchParams)).toEqual({
      limit: '200',
      cursor: 'a+b/c=',
      after: '0'
    });
  });
});

describe('response shape validation', () => {
  const invalidResponses: [string, unknown][] = [
    ['getConfig', null],
    ['getConfig', []],
    ['getConfig', 'secret-token'],
    ['getConfig', { debug: 'true' }],
    ['getConfig', { routing: [] }],
    ['getConfig', { 'request-retry': null }],
    ['getAuthFiles', {}],
    ['getAuthFiles', { files: {} }],
    ['getAuthFiles', { files: [null] }],
    ['getAuthFiles', { files: [{ name: 123 }] }],
    ['getAuthFiles', { files: [{ name: 'test.json', provider: {} }] }],
    ['getAuthFiles', { files: [{ name: 'test.json', success: 'secret-token' }] }],
    ['getAuthFiles', { files: [{ name: 'test.json', failed: -1 }] }],
    ['getAuthFiles', { files: [{ name: 'test.json', recent_requests: [null] }] }],
    ['getAPIKeys', {}],
    ['getAPIKeys', { 'api-keys': [null] }],
    ['getAPIKeys', { 'api-keys': 'secret-token' }],
    ['getAPIKeyUsage', null],
    ['getAPIKeyUsage', { provider: [] }],
    [
      'getAPIKeyUsage',
      { provider: { 'secret-token': { success: '1', failed: 0, recent_requests: [] } } }
    ],
    ['getPlugins', {}],
    ['getPlugins', { plugins_enabled: true, plugins_dir: 'plugins', plugins: [null] }],
    ['getLogs', { lines: [null], 'line-count': 1, 'latest-timestamp': 0, 'next-cursor': '' }],
    ['getLogs', { lines: ['secret-token'] }],
    ['getRoutingStrategy', { strategy: null }]
  ];

  it.each(invalidResponses)('rejects malformed %s data safely (%j)', async (method, body) => {
    const { client } = fixture(body);
    const operation = client[method as keyof typeof client] as () => Promise<unknown>;
    const error = await operation().catch((error: unknown) => error);
    expect(error).toBeInstanceOf(ManagementError);
    expect(error).toMatchObject({ code: 'invalid-response' });
    expect(String(error)).not.toContain('secret-token');
  });

  it('rejects numeric overflow in valid JSON rather than rendering infinity', async () => {
    const { client, fetcher } = fixture();
    fetcher.mockResolvedValue(new Response('{"files":[{"name":"test.json","success":1e999}]}'));
    await expect(client.getAuthFiles()).rejects.toMatchObject({ code: 'invalid-response' });
  });

  it('accepts sparse disk credentials and unknown fields for newer server versions', async () => {
    const value = { name: 'test.json', size: 42, extra: { future: true } };
    const { client } = fixture({ files: [value] });
    expect(await client.getAuthFiles()).toEqual([value]);
  });

  it('accepts an empty server configuration and documented negative retry settings', async () => {
    const { client, fetcher } = fixture({});
    expect(await client.getConfig()).toEqual({});
    fetcher.mockResolvedValue(new Response('{"max-retry-credentials":-1,"api-keys":null}'));
    expect(await client.getConfig()).toEqual({ 'max-retry-credentials': -1, 'api-keys': null });
  });

  it('rejects an empty successful GET response', async () => {
    const { client, fetcher } = fixture();
    fetcher.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(client.getConfig()).rejects.toMatchObject({ code: 'invalid-response' });
  });
});

it('uses the console session for device flows and paged request logs, never a browser management key', async () => {
  const { createSessionManagementClient } = await import('./client');
  const fetcher = vi.fn<typeof fetch>().mockImplementation(async (input) => {
    const url = String(input);
    if (url === '/api/codex/device-auth') return new Response(JSON.stringify({ status: 'idle' }));
    if (url === '/api/management/request-logs') return new Response(JSON.stringify({ files: [] }));
    return new Response(
      JSON.stringify({
        name: 'a.log',
        text: 'next',
        next_offset: 8,
        size: 8,
        modified: 1,
        has_more: false
      })
    );
  });
  const client = createSessionManagementClient(fetcher);
  await client.getCodexDeviceAuth();
  await client.startCodexDeviceAuth();
  await client.cancelCodexDeviceAuth();
  expect(fetcher.mock.calls.slice(0, 3).map((call) => call[1]?.method)).toEqual([
    'GET',
    'POST',
    'DELETE'
  ]);
  expect(await client.listRequestLogs()).toEqual([]);
  expect((await client.getRequestLogPreview('a.log', { offset: 4, limit: 4 })).text).toBe('next');
  expect(fetcher.mock.calls.at(-1)?.[0]).toBe(
    '/api/management/request-logs/a.log?offset=4&limit=4'
  );
  expect(client.getRequestLogDownloadURL('a.log')).toBe(
    '/api/management/request-logs/a.log/download'
  );
  for (const [, init] of fetcher.mock.calls) {
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).has('authorization')).toBe(false);
  }
});

it('rejects malformed device challenges and request-log pages', async () => {
  const { createSessionManagementClient } = await import('./client');
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
    new Response(
      JSON.stringify({
        status: 'pending',
        verification_uri: 'https://attacker.example',
        user_code: 'code'
      })
    )
  );
  await expect(createSessionManagementClient(fetcher).startCodexDeviceAuth()).rejects.toMatchObject(
    { code: 'invalid-response' }
  );
  const invalidPage = vi.fn<typeof fetch>().mockResolvedValue(
    new Response(
      JSON.stringify({
        name: 'a.log',
        text: 'text',
        next_offset: -1,
        size: 1,
        modified: 1,
        has_more: false
      })
    )
  );
  await expect(
    createSessionManagementClient(invalidPage).getRequestLogPreview('a.log')
  ).rejects.toMatchObject({ code: 'invalid-response' });
});
