import { afterEach, expect, it, vi } from 'vitest';
import { AuthStore, SESSION_SECONDS } from './auth';
import { relayEvents } from './events';
const config = {
  passwordHash: 'unused',
  managementURL: 'http://private/v0/management',
  managementKey: 'private-key'
};
const encoder = new TextEncoder();
afterEach(() => vi.useRealTimers());
function fixture() {
  const sessions = new AuthStore();
  const token = sessions.createSession('test')!;
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const cancelled = vi.fn();
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
    new Response(
      new ReadableStream({
        start(c) {
          controller = c;
        },
        cancel: cancelled
      }),
      { headers: { 'Content-Type': 'text/event-stream', 'Set-Cookie': 'secret=upstream' } }
    )
  );
  const request = new Request('https://console/api/management/events', {
    headers: { cookie: 'browser=secret' }
  });
  return {
    sessions,
    token,
    fetcher,
    request,
    cancelled,
    send: (data: string) => controller.enqueue(encoder.encode(data))
  };
}
it('streams chunks immediately, keeps credentials private and aborts upstream on browser cancellation', async () => {
  const f = fixture();
  const response = await relayEvents(
    f.request,
    f.sessions.identity(f.token)!,
    config,
    f.fetcher,
    f.sessions
  );
  const reader = response.body!.getReader();
  f.send('data: 1\n\n');
  expect(new TextDecoder().decode((await reader.read()).value)).toBe('data: 1\n\n');
  const [url, init] = f.fetcher.mock.calls[0];
  expect(url).toBe('http://private/v0/management/events');
  expect(new Headers(init?.headers).get('authorization')).toBe('Bearer private-key');
  expect(new Headers(init?.headers).has('cookie')).toBe(false);
  expect(response.headers.has('set-cookie')).toBe(false);
  await reader.cancel();
  expect(init?.signal?.aborted).toBe(true);
  expect(f.cancelled).toHaveBeenCalled();
});
it.each(['logout', 'expiry'])('closes existing streams on %s without polling', async (reason) => {
  vi.useFakeTimers();
  const f = fixture();
  const response = await relayEvents(
    f.request,
    f.sessions.identity(f.token)!,
    config,
    f.fetcher,
    f.sessions
  );
  const reader = response.body!.getReader();
  if (reason === 'logout') f.sessions.revoke(f.token);
  else await vi.advanceTimersByTimeAsync(SESSION_SECONDS * 1000);
  expect(new TextDecoder().decode((await reader.read()).value)).toContain('event: session-ended');
  expect((await reader.read()).done).toBe(true);
  expect(f.fetcher.mock.calls[0][1]?.signal?.aborted).toBe(true);
});
it('rejects a revoked session before opening an upstream stream', async () => {
  const f = fixture();
  const identity = f.sessions.identity(f.token)!;
  f.sessions.revoke(f.token);
  const response = await relayEvents(f.request, identity, config, f.fetcher, f.sessions);
  expect(response.status).toBe(401);
  expect(f.fetcher).not.toHaveBeenCalled();
});
