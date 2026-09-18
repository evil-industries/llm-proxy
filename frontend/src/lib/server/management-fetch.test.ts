import { createServer } from 'node:http';
import { once } from 'node:events';
import type { AddressInfo } from 'node:net';
import { Client, getGlobalDispatcher, type Dispatcher } from 'undici';
import { expect, it, vi } from 'vitest';
import { relayManagement } from './relay';

it('disables response deadlines on actual management dispatches without changing global fetch', async () => {
  const globalDispatcher = getGlobalDispatcher();
  const dispatched: Dispatcher.DispatchOptions[] = [];
  const dispatch = Client.prototype.dispatch;
  const spy = vi.spyOn(Client.prototype, 'dispatch').mockImplementation(function (
    this: Client,
    options,
    handler
  ) {
    dispatched.push(options);
    return dispatch.call(this, options, handler);
  });
  const upstream = createServer((request, response) => {
    expect(request.headers.authorization).toBe('Bearer private-test-key');
    response.setHeader('Content-Type', 'application/json');
    response.end('{"debug":true}');
  });
  upstream.listen(0, '127.0.0.1');
  await once(upstream, 'listening');
  try {
    const response = await relayManagement(
      new Request('https://console.example/api/management/config'),
      'config',
      {
        passwordHash: 'unused',
        managementKey: 'private-test-key',
        managementURL: `http://127.0.0.1:${(upstream.address() as AddressInfo).port}/v0/management`
      }
    );
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ debug: true });
    expect(dispatched).toHaveLength(1);
    // Assert the final client request, after fetch and all dispatcher defaults.
    expect(dispatched[0]).toMatchObject({
      path: '/v0/management/config',
      headersTimeout: 0,
      bodyTimeout: 0
    });
    expect(getGlobalDispatcher()).toBe(globalDispatcher);
  } finally {
    spy.mockRestore();
    upstream.closeAllConnections();
    await new Promise<void>((resolve) => upstream.close(() => resolve()));
  }
});
