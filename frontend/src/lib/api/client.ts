import type {
  APIKeyUsage,
  AuthFile,
  BooleanSetting,
  LogsQuery,
  LogsResponse,
  ManagementConfig,
  NotificationSettings,
  NotificationUpdate,
  PluginList,
  RoutingStrategy
} from './types';
import { validManagementResponse } from './validation';

export type ManagementErrorCode =
  | 'authentication'
  | 'forbidden'
  | 'not-found'
  | 'invalid-request'
  | 'rate-limit'
  | 'server'
  | 'network'
  | 'invalid-response';

/** Errors intentionally exclude upstream bodies, URLs and credential-bearing data. */
export class ManagementError extends Error {
  constructor(
    public readonly code: ManagementErrorCode,
    message: string,
    public readonly status?: number
  ) {
    super(message);
    this.name = 'ManagementError';
  }
}

function responseError(status: number): ManagementError {
  if (status === 401)
    return new ManagementError(
      'authentication',
      'The management key was not accepted. Check your key and reconnect.',
      status
    );
  if (status === 403)
    return new ManagementError(
      'forbidden',
      'Management access is not allowed from this address. Check the server’s remote management settings.',
      status
    );
  if (status === 404)
    return new ManagementError(
      'not-found',
      'This management resource is unavailable. Check the server address and management configuration.',
      status
    );
  if (status === 429)
    return new ManagementError(
      'rate-limit',
      'Too many requests. Wait a moment before trying again.',
      status
    );
  if (status >= 500)
    return new ManagementError(
      'server',
      'The server could not complete this request. Try again or check the server logs.',
      status
    );
  return new ManagementError(
    'invalid-request',
    'The server rejected this change. Check the values and try again.',
    status
  );
}

/** Accept a server origin or a URL ending in /v0/management. */
export function managementBaseURL(value: string): string {
  const input = value.trim().replace(/\/+$/, '');
  if (!input) return '/v0/management';
  let url: URL;
  try {
    url = new URL(input);
  } catch {
    throw new ManagementError(
      'invalid-request',
      'Enter a valid http:// or https:// server address.'
    );
  }
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  ) {
    throw new ManagementError(
      'invalid-request',
      'Use an HTTP or HTTPS server address without credentials, a query, or a fragment.'
    );
  }
  const path = url.pathname.replace(/\/+$/, '');
  url.pathname = path.endsWith('/v0/management') ? path : `${path}/v0/management`;
  return url.toString().replace(/\/+$/, '');
}

export interface ManagementClientOptions {
  baseUrl: string;
  managementKey: string;
  fetch?: typeof globalThis.fetch;
}

export function createManagementClient(options: ManagementClientOptions) {
  const base = managementBaseURL(options.baseUrl);
  const key = options.managementKey.trim();
  const fetcher = options.fetch ?? globalThis.fetch;
  if (!key) throw new ManagementError('authentication', 'Enter your management key to connect.');

  return createClient(base, key, fetcher);
}

/** Same-origin, cookie-authenticated client. The backend key never reaches the browser. */
export function createSessionManagementClient(fetcher: typeof globalThis.fetch = globalThis.fetch) {
  return createClient('/api/management', undefined, fetcher);
}

function createClient(base: string, key: string | undefined, fetcher: typeof globalThis.fetch) {
  async function request<T>(
    path: string,
    method = 'GET',
    body?: unknown,
    signal?: AbortSignal
  ): Promise<T> {
    const multipart = body instanceof FormData;
    const headers = new Headers({ Accept: 'application/json' });
    if (key) headers.set('Authorization', `Bearer ${key}`);
    if (body !== undefined && !multipart) headers.set('Content-Type', 'application/json');
    let response: Response;
    try {
      response = await fetcher(`${base}${path}`, {
        method,
        headers,
        signal,
        cache: 'no-store',
        credentials: key ? 'omit' : 'same-origin',
        redirect: 'error',
        body: body === undefined ? undefined : multipart ? body : JSON.stringify(body)
      });
    } catch (error) {
      if (signal?.aborted || (error instanceof Error && error.name === 'AbortError')) throw error;
      throw new ManagementError(
        'network',
        'Unable to reach the server. Check the address, connection, and browser access settings.'
      );
    }
    if (!key && response.status === 401) {
      throw new ManagementError('authentication', 'Your session has expired. Sign in again.', 401);
    }
    if (!response.ok) throw responseError(response.status);
    if (response.status === 204 && method !== 'GET') return undefined as T;
    try {
      const value: unknown = await response.json();
      if (
        (method === 'GET' || path === '/notifications') &&
        !validManagementResponse(path, value)
      ) {
        throw new Error('Invalid response shape');
      }
      return value as T;
    } catch (error) {
      if (signal?.aborted || (error instanceof Error && error.name === 'AbortError')) throw error;
      throw new ManagementError(
        'invalid-response',
        'The server returned an unexpected response. Check the management server configuration.'
      );
    }
  }

  return {
    getNotifications: (signal?: AbortSignal) =>
      request<NotificationSettings>('/notifications', 'GET', undefined, signal),
    setNotifications: (settings: NotificationUpdate, signal?: AbortSignal) =>
      request<NotificationSettings>('/notifications', 'PUT', settings, signal),
    testNotifications: (signal?: AbortSignal) =>
      request<void>('/notifications/test', 'POST', {}, signal),
    getConfig: (signal?: AbortSignal) =>
      request<ManagementConfig>('/config', 'GET', undefined, signal),
    async getAuthFiles(signal?: AbortSignal): Promise<AuthFile[]> {
      const response = await request<{ files: AuthFile[] | null }>(
        '/auth-files',
        'GET',
        undefined,
        signal
      );
      return response.files ?? [];
    },
    async getAPIKeys(signal?: AbortSignal): Promise<string[]> {
      const response = await request<{ 'api-keys': string[] | null }>(
        '/api-keys',
        'GET',
        undefined,
        signal
      );
      return response['api-keys'] ?? [];
    },
    getAPIKeyUsage: (signal?: AbortSignal) =>
      request<APIKeyUsage>('/api-key-usage', 'GET', undefined, signal),
    getPlugins: (signal?: AbortSignal) => request<PluginList>('/plugins', 'GET', undefined, signal),
    getLogs(query: LogsQuery = {}, signal?: AbortSignal) {
      const params = new URLSearchParams();
      if (query.limit !== undefined) params.set('limit', String(query.limit));
      if (query.cursor) params.set('cursor', query.cursor);
      if (query.after !== undefined) params.set('after', String(query.after));
      return request<LogsResponse>(
        `/logs${params.size ? `?${params}` : ''}`,
        'GET',
        undefined,
        signal
      );
    },
    async getRoutingStrategy(signal?: AbortSignal): Promise<RoutingStrategy> {
      const response = await request<{ strategy: RoutingStrategy }>(
        '/routing/strategy',
        'GET',
        undefined,
        signal
      );
      return response.strategy;
    },
    setAuthFileDisabled: (
      name: string,
      disabled: boolean,
      authIndex?: string,
      signal?: AbortSignal
    ) =>
      request<void>(
        '/auth-files/status',
        'PATCH',
        { name, disabled, ...(authIndex ? { auth_index: authIndex } : {}) },
        signal
      ),
    uploadAuthFile(file: File, signal?: AbortSignal) {
      const data = new FormData();
      data.append('file', file);
      return request<void>('/auth-files', 'POST', data, signal);
    },
    deleteAuthFile: (name: string, signal?: AbortSignal) =>
      request<void>(`/auth-files?${new URLSearchParams({ name })}`, 'DELETE', undefined, signal),
    addAPIKey: (value: string, signal?: AbortSignal) =>
      request<void>('/api-keys/mutate', 'POST', { action: 'add', value }, signal),
    removeAPIKey: (value: string, signal?: AbortSignal) =>
      request<void>('/api-keys/mutate', 'POST', { action: 'remove', value }, signal),
    replaceAPIKeys: (keys: string[], signal?: AbortSignal) =>
      request<void>('/api-keys', 'PUT', keys, signal),
    updateAPIKey: (index: number, value: string, signal?: AbortSignal) =>
      request<void>('/api-keys', 'PATCH', { index, value }, signal),
    deleteAPIKey: (index: number, signal?: AbortSignal) =>
      request<void>(
        `/api-keys?${new URLSearchParams({ index: String(index) })}`,
        'DELETE',
        undefined,
        signal
      ),
    setBoolean: (setting: BooleanSetting, value: boolean, signal?: AbortSignal) =>
      request<void>(`/${setting}`, 'PUT', { value }, signal),
    setRoutingStrategy: (value: RoutingStrategy, signal?: AbortSignal) =>
      request<void>('/routing/strategy', 'PUT', { value }, signal),
    setPluginEnabled: (id: string, enabled: boolean, signal?: AbortSignal) =>
      request<void>(`/plugins/${encodeURIComponent(id)}/enabled`, 'PATCH', { enabled }, signal)
  };
}

export type ManagementClient = ReturnType<typeof createManagementClient>;
