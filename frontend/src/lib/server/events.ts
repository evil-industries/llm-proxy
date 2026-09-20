import { authStore } from './auth';
import type { ServerConfiguration } from './config';
import { managementFetch } from './management-fetch';
import { safeJSON } from './relay';

export async function relayEvents(
  request: Request,
  identity: string,
  config: ServerConfiguration,
  fetcher: typeof fetch = managementFetch,
  sessions = authStore
): Promise<Response> {
  const abort = new AbortController();
  let ended = false;
  let controller: ReadableStreamDefaultController<Uint8Array> | undefined;
  let unwatch = () => {};
  const cleanup = () => {
    abort.abort();
    unwatch();
    request.signal.removeEventListener('abort', disconnect);
  };
  const disconnect = () => {
    if (ended) return;
    ended = true;
    controller?.close();
    cleanup();
  };
  request.signal.addEventListener('abort', disconnect, { once: true });
  unwatch = sessions.watch(identity, () => {
    if (ended) return;
    controller?.enqueue(new TextEncoder().encode('event: session-ended\ndata: {}\n\n'));
    disconnect();
  });
  if (request.signal.aborted || ended) {
    cleanup();
    return safeJSON({ error: 'Session ended.' }, 401);
  }
  try {
    const upstream = await fetcher(`${config.managementURL}/events`, {
      headers: { Authorization: `Bearer ${config.managementKey}`, Accept: 'text/event-stream' },
      signal: abort.signal,
      redirect: 'error',
      cache: 'no-store'
    });
    if (
      !upstream.ok ||
      !upstream.body ||
      !upstream.headers.get('content-type')?.startsWith('text/event-stream')
    ) {
      await upstream.body?.cancel();
      cleanup();
      return safeJSON({ error: 'Live updates are unavailable.' }, 502);
    }
    const reader = upstream.body.getReader();
    const body = new ReadableStream<Uint8Array>({
      start(stream) {
        controller = stream;
        if (ended) stream.close();
      },
      async pull(stream) {
        try {
          const { done, value } = await reader.read();
          if (ended) return;
          if (done) disconnect();
          else stream.enqueue(value);
        } catch {
          if (!ended) {
            ended = true;
            stream.error(new Error('Live updates disconnected.'));
            cleanup();
          }
        }
      },
      cancel() {
        ended = true;
        cleanup();
        return reader.cancel().catch(() => {});
      }
    });
    return new Response(body, {
      headers: {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache, no-store',
        'X-Accel-Buffering': 'no'
      }
    });
  } catch {
    cleanup();
    return safeJSON({ error: 'Live updates are unavailable.' }, ended ? 401 : 502);
  }
}
