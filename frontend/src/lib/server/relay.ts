import type { ServerConfiguration } from './config';
import { sameOrigin } from './auth';

const METHODS: Record<string, readonly string[]> = {
  config: ['GET'],
  'auth-files': ['GET', 'POST', 'DELETE'],
  'auth-files/status': ['PATCH'],
  'api-keys': ['GET', 'PUT', 'PATCH', 'DELETE'],
  'api-keys/mutate': ['POST'],
  logs: ['GET'],
  notifications: ['GET', 'PUT'],
  'notifications/test': ['POST'],
  'routing/strategy': ['GET', 'PUT'],
  debug: ['PUT'],
  'logging-to-file': ['PUT'],
  'usage-statistics-enabled': ['PUT'],
  'request-log': ['PUT'],
  'ws-auth': ['PUT'],
  'force-model-prefix': ['PUT'],
  'quota-exceeded/switch-project': ['PUT'],
  'quota-exceeded/switch-preview-model': ['PUT']
};

export const safeJSON = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }
  });

export class BodyLimitError extends Error {}

export async function boundedBody(
  stream: ReadableStream<Uint8Array> | null,
  maximum: number
): Promise<Uint8Array> {
  if (!stream) return new Uint8Array();
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > maximum) {
        await reader.cancel();
        throw new BodyLimitError();
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  const result = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return result;
}

function validQuery(path: string, method: string, params: URLSearchParams): boolean {
  const allowed =
    path === 'logs'
      ? ['limit', 'cursor', 'after']
      : path === 'auth-files' && method === 'GET'
        ? ['name', 'auth_index']
        : path === 'auth-files' && method === 'DELETE'
          ? ['name']
          : path === 'api-keys' && method === 'DELETE'
            ? ['index']
            : [];
  if ([...params.keys()].some((key) => !allowed.includes(key) || params.getAll(key).length !== 1))
    return false;
  if (method === 'DELETE' && path === 'auth-files') return Boolean(params.get('name'));
  if (method === 'DELETE' && path === 'api-keys') return /^\d+$/.test(params.get('index') ?? '');
  return true;
}

export async function relayManagement(
  request: Request,
  path: string,
  config: ServerConfiguration,
  fetcher: typeof fetch = fetch
): Promise<Response> {
  const url = new URL(request.url);
  const method = request.method;
  if (!Object.hasOwn(METHODS, path) || !METHODS[path].includes(method))
    return safeJSON({ error: 'Management endpoint is not available.' }, 404);
  if (method !== 'GET' && !sameOrigin(request, url))
    return safeJSON({ error: 'Request origin was not accepted.' }, 403);
  if (!validQuery(path, method, url.searchParams))
    return safeJSON({ error: 'Invalid management query.' }, 400);
  const headers = new Headers({
    Authorization: `Bearer ${config.managementKey}`,
    Accept: 'application/json'
  });
  let body: ReadableStream<Uint8Array> | null = null;
  if (['POST', 'PUT', 'PATCH'].includes(method)) {
    const contentType = request.headers.get('content-type') ?? '';
    const multipart =
      path === 'auth-files' &&
      method === 'POST' &&
      /^multipart\/form-data;\s*boundary=/i.test(contentType);
    if (!multipart && !/^application\/json(?:\s*;|$)/i.test(contentType))
      return safeJSON({ error: 'Use JSON or a credential file upload.' }, 415);
    // Authenticated operators may upload arbitrary-sized management payloads.
    // Stream them to Go instead of buffering the whole request in Node.
    body = request.body;
    headers.set('Content-Type', contentType);
  }
  try {
    const init: RequestInit & { duplex: 'half' } = {
      method,
      headers,
      body,
      duplex: 'half',
      redirect: 'error',
      credentials: 'omit',
      cache: 'no-store',
      signal: request.signal
    };
    const upstream = await fetcher(`${config.managementURL}/${path}${url.search}`, init);
    if (!upstream.ok) {
      await upstream.body?.cancel();
      const status =
        upstream.status === 401 || upstream.status === 403 || upstream.status >= 500
          ? 502
          : upstream.status;
      return safeJSON({ error: 'The proxy server could not complete this request.' }, status);
    }
    if (upstream.status === 204)
      return new Response(null, { status: 204, headers: { 'Cache-Control': 'no-store' } });
    let data: unknown;
    try {
      data = await upstream.json();
    } catch {
      return safeJSON({ error: 'The proxy server returned an invalid response.' }, 502);
    }
    return safeJSON(data, upstream.status);
  } catch {
    return safeJSON({ error: 'Unable to reach the configured proxy server.' }, 502);
  }
}
