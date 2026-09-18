import type { CodexDeviceAuth } from '$lib/api/types';
import { validCodexDeviceAuth } from '$lib/api/validation';
import type { ServerConfiguration } from './config';
import { managementFetch } from './management-fetch';
import { SESSION_SECONDS } from './auth';

type Operation = 'GET' | 'POST' | 'DELETE';
type Session = {
  state?: string;
  result: CodexDeviceAuth;
  expires: number;
  closed: boolean;
  queue: Promise<unknown>;
};

export class DeviceAuthError extends Error {
  constructor(
    public readonly status: number,
    message: string
  ) {
    super(message);
  }
}

/** Backend flow identifiers never leave this console-session-bound store. */
export class DeviceAuthSessions {
  private sessions = new Map<string, Session>();
  constructor(
    private readonly fetcher: typeof fetch = managementFetch,
    private readonly now: () => number = Date.now
  ) {}

  private async upstream(config: ServerConfiguration, method: Operation, state?: string) {
    let response: Response;
    try {
      response = await this.fetcher(
        `${config.managementURL}/codex/device-auth${state ? `?${new URLSearchParams({ state })}` : ''}`,
        {
          method,
          headers: {
            Authorization: `Bearer ${config.managementKey}`,
            Accept: 'application/json',
            ...(method === 'POST' ? { 'Content-Type': 'application/json' } : {})
          },
          body: method === 'POST' ? '{}' : undefined,
          redirect: 'error',
          cache: 'no-store',
          // These requests acquire/cancel credentials, never carry inference traffic.
          signal: AbortSignal.timeout(30_000)
        }
      );
    } catch {
      throw new DeviceAuthError(
        502,
        'The gateway could not be reached. Retry to check your existing login attempt.'
      );
    }
    if (!response.ok) {
      await response.body?.cancel();
      if (response.status === 404 && state)
        return {
          result: {
            status: 'error',
            error: 'This login attempt is no longer available. Start a new device login.'
          } as CodexDeviceAuth
        };
      throw new DeviceAuthError(
        502,
        method === 'POST'
          ? 'Device login could not start. Check your gateway connection and enable device code login in your ChatGPT security settings.'
          : 'The gateway could not check this login attempt. Try again.'
      );
    }
    let raw: unknown;
    try {
      raw = await response.json();
    } catch {
      throw new DeviceAuthError(502, 'The gateway returned an invalid device login response.');
    }
    if (!validCodexDeviceAuth(raw))
      throw new DeviceAuthError(502, 'The gateway returned an invalid device login response.');
    const data = raw as CodexDeviceAuth & { state?: unknown };
    if (
      method === 'POST' &&
      (typeof data.state !== 'string' || !/^[a-zA-Z0-9_-]{16,256}$/.test(data.state))
    )
      throw new DeviceAuthError(502, 'The gateway returned an invalid device login identifier.');
    const result: CodexDeviceAuth = { status: data.status };
    if (data.status === 'pending') {
      result.user_code = data.user_code;
      result.verification_uri = data.verification_uri;
      result.expires_at = data.expires_at;
      result.interval = data.interval;
    }
    if (data.account)
      result.account = {
        name: data.account.name,
        ...(data.account.email ? { email: data.account.email } : {}),
        ...(data.account.plan_type ? { plan_type: data.account.plan_type } : {})
      };
    if (data.status === 'error')
      result.error = 'Device login failed. Check your account permissions and start a new login.';
    if (data.status === 'expired')
      result.error = 'The device code expired. Start a new login to get another code.';
    return { result, state: typeof data.state === 'string' ? data.state : state };
  }

  async run(
    owner: string,
    method: Operation,
    config: ServerConfiguration
  ): Promise<CodexDeviceAuth> {
    for (const [key, entry] of this.sessions) {
      if (entry.expires <= this.now()) {
        entry.closed = true;
        this.sessions.delete(key);
      }
    }
    let session = this.sessions.get(owner);
    if (!session) {
      if (method !== 'POST') return { status: 'idle' };
      session = {
        result: { status: 'idle' },
        expires: this.now() + SESSION_SECONDS * 1000,
        closed: false,
        queue: Promise.resolve()
      };
      this.sessions.set(owner, session);
    }
    const entry = session;
    const operation = entry.queue
      .catch(() => {})
      .then(async () => {
        if (entry.closed) return { status: 'idle' } as CodexDeviceAuth;
        // A repeated start resumes an existing approval instead of orphaning it.
        if (entry.state && entry.result.status === 'pending') {
          const current = await this.upstream(
            config,
            method === 'DELETE' ? 'DELETE' : 'GET',
            entry.state
          );
          entry.result = current.result;
          if (entry.closed) return { status: 'idle' } as CodexDeviceAuth;
          if (
            method !== 'POST' ||
            current.result.status === 'pending' ||
            current.result.status === 'complete'
          )
            return current.result;
        }
        if (method === 'POST') {
          const started = await this.upstream(config, 'POST');
          entry.state = started.state;
          entry.result = started.result;
          if (entry.closed) return { status: 'idle' } as CodexDeviceAuth;
        }
        return entry.result;
      });
    entry.queue = operation;
    return operation;
  }

  async forget(owner: string | undefined, config: ServerConfiguration | null) {
    if (!owner) return;
    const entry = this.sessions.get(owner);
    if (!entry) return;
    entry.closed = true;
    this.sessions.delete(owner);
    try {
      await entry.queue;
    } catch {
      /* A failed poll must not prevent cancellation. */
    }
    try {
      if (config && entry.state && entry.result.status === 'pending')
        await this.upstream(config, 'DELETE', entry.state);
    } catch {
      /* The backend flow has its own bounded credential-acquisition lifetime. */
    }
  }
}

export const deviceAuthSessions = new DeviceAuthSessions();
