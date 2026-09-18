import { describe, expect, it, vi } from 'vitest';
import type { ServerConfiguration } from './config';
import { SESSION_SECONDS } from './auth';

vi.mock('$lib/api/validation', () => import('../api/validation'));
import { DeviceAuthError, DeviceAuthSessions } from './device-auth';

const config: ServerConfiguration = {
  passwordHash: 'unused',
  managementURL: 'http://private-gateway:8317/v0/management',
  managementKey: 'private-management-key'
};
const backendState = 's'.repeat(43);
const secret = 'private-access-token-never-return';
const pending = (state = backendState) => ({
  state,
  status: 'pending',
  verification_uri: 'https://auth.openai.com/codex/device',
  user_code: 'ABCD-EFGH',
  expires_at: '2030-01-01T12:15:00Z',
  interval: 5
});
const publicPending = () => {
  const { state: _, ...result } = pending();
  return result;
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}
function methods(fetcher: ReturnType<typeof vi.fn<typeof fetch>>) {
  return fetcher.mock.calls.map(([, options]) => options?.method);
}

describe('console-session-bound device approval', () => {
  it('starts with server credentials and returns only the public approval fields', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      Response.json({
        ...pending(),
        access_token: secret,
        refresh_token: secret,
        device_auth_id: secret
      })
    );
    const sessions = new DeviceAuthSessions(fetcher);
    const result = await sessions.run('hashed-console-owner', 'POST', config);
    expect(result).toEqual(publicPending());
    expect(JSON.stringify(result)).not.toContain(secret);
    expect(JSON.stringify(result)).not.toContain(backendState);
    const [url, options] = fetcher.mock.calls[0];
    expect(url).toBe(`${config.managementURL}/codex/device-auth`);
    expect(options).toMatchObject({
      method: 'POST',
      body: '{}',
      redirect: 'error',
      cache: 'no-store'
    });
    expect(new Headers(options?.headers).get('authorization')).toBe(
      `Bearer ${config.managementKey}`
    );
    expect(new Headers(options?.headers).get('cookie')).toBeNull();
  });

  it('isolates independent browser sessions, including polling and cancellation', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending()))
      .mockResolvedValueOnce(Response.json(pending('t'.repeat(43))))
      .mockResolvedValueOnce(Response.json({ status: 'cancelled' }))
      .mockResolvedValueOnce(Response.json(pending('t'.repeat(43))));
    const sessions = new DeviceAuthSessions(fetcher);
    await sessions.run('owner-a', 'POST', config);
    expect(await sessions.run('owner-b', 'GET', config)).toEqual({ status: 'idle' });
    expect(await sessions.run('owner-b', 'DELETE', config)).toEqual({ status: 'idle' });
    expect(fetcher).toHaveBeenCalledTimes(1);
    await sessions.run('owner-b', 'POST', config);
    await sessions.run('owner-a', 'DELETE', config);
    expect(await sessions.run('owner-b', 'GET', config)).toMatchObject({ status: 'pending' });
    expect(new URL(String(fetcher.mock.calls[2][0])).searchParams.get('state')).toBe(backendState);
    expect(new URL(String(fetcher.mock.calls[3][0])).searchParams.get('state')).toBe(
      't'.repeat(43)
    );
  });

  it('serializes simultaneous starts and resumes the original code rather than creating another flow', async () => {
    const started = deferred<Response>();
    const entered = deferred<void>();
    const fetcher = vi
      .fn<typeof fetch>()
      .mockImplementationOnce(() => {
        entered.resolve();
        return started.promise;
      })
      .mockResolvedValueOnce(Response.json(pending()));
    const sessions = new DeviceAuthSessions(fetcher);
    const first = sessions.run('owner', 'POST', config);
    const second = sessions.run('owner', 'POST', config);
    await entered.promise;
    expect(fetcher).toHaveBeenCalledTimes(1);
    started.resolve(Response.json(pending()));
    expect(await first).toEqual(publicPending());
    expect(await second).toEqual(publicPending());
    expect(methods(fetcher)).toEqual(['POST', 'GET']);
    expect(new URL(String(fetcher.mock.calls[1][0])).searchParams.get('state')).toBe(backendState);
  });

  it('retries a failed poll using the existing flow and preserves its code', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending()))
      .mockRejectedValueOnce(new Error(secret))
      .mockResolvedValueOnce(Response.json(pending()));
    const sessions = new DeviceAuthSessions(fetcher);
    await sessions.run('owner', 'POST', config);
    await expect(sessions.run('owner', 'GET', config)).rejects.toMatchObject({ status: 502 });
    expect(await sessions.run('owner', 'POST', config)).toEqual(publicPending());
    expect(methods(fetcher)).toEqual(['POST', 'GET', 'GET']);
    expect(fetcher.mock.calls[2][0]).toBe(fetcher.mock.calls[1][0]);
  });

  it('starts a fresh attempt only after the existing attempt reaches a failed terminal state', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending()))
      .mockResolvedValueOnce(Response.json({ status: 'expired', error: secret }))
      .mockResolvedValueOnce(Response.json(pending('t'.repeat(43))));
    const sessions = new DeviceAuthSessions(fetcher);
    await sessions.run('owner', 'POST', config);
    expect(await sessions.run('owner', 'POST', config)).toMatchObject({ status: 'pending' });
    expect(methods(fetcher)).toEqual(['POST', 'GET', 'POST']);
  });

  it('returns a completed resumed attempt without starting another login and strips token fields', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending()))
      .mockResolvedValueOnce(
        Response.json({
          status: 'complete',
          access_token: secret,
          state: backendState,
          account: {
            name: 'account.json',
            email: 'operator@example.test',
            plan_type: 'team',
            refresh_token: secret
          }
        })
      );
    const sessions = new DeviceAuthSessions(fetcher);
    await sessions.run('owner', 'POST', config);
    const result = await sessions.run('owner', 'POST', config);
    expect(result).toEqual({
      status: 'complete',
      account: { name: 'account.json', email: 'operator@example.test', plan_type: 'team' }
    });
    expect(await sessions.run('owner', 'GET', config)).toEqual(result);
    expect(methods(fetcher)).toEqual(['POST', 'GET']);
    expect(JSON.stringify(result)).not.toContain(secret);
  });

  it('cancels the flow created by a start still in flight', async () => {
    const started = deferred<Response>();
    const entered = deferred<void>();
    const fetcher = vi
      .fn<typeof fetch>()
      .mockImplementationOnce(() => {
        entered.resolve();
        return started.promise;
      })
      .mockResolvedValueOnce(Response.json({ status: 'cancelled' }));
    const sessions = new DeviceAuthSessions(fetcher);
    const first = sessions.run('owner', 'POST', config);
    await entered.promise;
    const cancellation = sessions.run('owner', 'DELETE', config);
    started.resolve(Response.json(pending()));
    await first;
    expect(await cancellation).toEqual({ status: 'cancelled' });
    expect(await sessions.run('owner', 'GET', config)).toEqual({ status: 'cancelled' });
    expect(methods(fetcher)).toEqual(['POST', 'DELETE']);
  });

  it('forgets an in-flight start on logout and cancels the eventual backend flow exactly once', async () => {
    const started = deferred<Response>();
    const entered = deferred<void>();
    const fetcher = vi
      .fn<typeof fetch>()
      .mockImplementationOnce(() => {
        entered.resolve();
        return started.promise;
      })
      .mockResolvedValue(Response.json({ status: 'cancelled' }));
    const sessions = new DeviceAuthSessions(fetcher);
    const first = sessions.run('owner', 'POST', config);
    await entered.promise;
    const logout = sessions.forget('owner', config);
    started.resolve(Response.json(pending()));
    expect(await first).toEqual({ status: 'idle' });
    await logout;
    expect(await sessions.run('owner', 'GET', config)).toEqual({ status: 'idle' });
    expect(methods(fetcher)).toEqual(['POST', 'DELETE']);
  });

  it('does not return a pending approval after logout during an in-flight poll', async () => {
    const polled = deferred<Response>();
    const entered = deferred<void>();
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending()))
      .mockImplementationOnce(() => {
        entered.resolve();
        return polled.promise;
      })
      .mockResolvedValueOnce(Response.json({ status: 'cancelled' }));
    const sessions = new DeviceAuthSessions(fetcher);
    await sessions.run('owner', 'POST', config);
    const poll = sessions.run('owner', 'GET', config);
    await entered.promise;
    const logout = sessions.forget('owner', config);
    polled.resolve(Response.json(pending()));
    expect(await poll).toEqual({ status: 'idle' });
    await logout;
    expect(methods(fetcher)).toEqual(['POST', 'GET', 'DELETE']);
  });

  it('still cancels a known flow when the poll in flight during logout fails', async () => {
    const polled = deferred<Response>();
    const entered = deferred<void>();
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending()))
      .mockImplementationOnce(() => {
        entered.resolve();
        return polled.promise;
      })
      .mockResolvedValueOnce(Response.json({ status: 'cancelled' }));
    const sessions = new DeviceAuthSessions(fetcher);
    await sessions.run('owner', 'POST', config);
    const poll = sessions.run('owner', 'GET', config);
    const settled = poll.catch(() => undefined);
    await entered.promise;
    const logout = sessions.forget('owner', config);
    polled.reject(new Error(secret));
    await settled;
    await logout;
    expect(methods(fetcher)).toEqual(['POST', 'GET', 'DELETE']);
    expect(await sessions.run('owner', 'GET', config)).toEqual({ status: 'idle' });
  });

  it('expires ownership records using a controllable clock', async () => {
    let now = 0;
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(Response.json(pending()));
    const sessions = new DeviceAuthSessions(fetcher, () => now);
    await sessions.run('owner', 'POST', config);
    now = SESSION_SECONDS * 1000;
    expect(await sessions.run('owner', 'GET', config)).toEqual({ status: 'idle' });
    expect(await sessions.run('owner', 'DELETE', config)).toEqual({ status: 'idle' });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});

describe('untrusted device login responses', () => {
  it.each([
    { ...pending(), state: undefined },
    { ...pending(), state: 'bad?state=value' },
    { ...pending(), verification_uri: 'https://attacker.example/steal' },
    { ...pending(), expires_at: secret },
    { ...pending(), interval: 0 },
    { ...pending(), user_code: '' },
    { status: 'unknown', access_token: secret }
  ])('rejects malformed start responses without leaking payloads %#', async (payload) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(Response.json(payload));
    const sessions = new DeviceAuthSessions(fetcher);
    const error = await sessions.run('owner', 'POST', config).catch((error: unknown) => error);
    expect(error).toBeInstanceOf(DeviceAuthError);
    expect(error).toMatchObject({ status: 502 });
    expect(String(error)).not.toContain(secret);
    expect(String(error)).not.toContain('attacker.example');
  });

  it('sanitizes backend errors and network failures, and permits a subsequent retry', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(secret, { status: 401 }))
      .mockRejectedValueOnce(new Error(`${config.managementURL} ${config.managementKey} ${secret}`))
      .mockResolvedValueOnce(new Response(secret))
      .mockResolvedValueOnce(Response.json(pending()));
    const sessions = new DeviceAuthSessions(fetcher);
    for (let attempt = 0; attempt < 3; attempt++) {
      const error = await sessions.run('owner', 'POST', config).catch((error: unknown) => error);
      expect(error).toMatchObject({ status: 502 });
      expect(String(error)).not.toContain(secret);
      expect(String(error)).not.toContain(config.managementKey);
      expect(String(error)).not.toContain(config.managementURL);
    }
    expect(await sessions.run('owner', 'POST', config)).toEqual(publicPending());
  });

  it('replaces provider error details with a safe message and retains no approval code', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending()))
      .mockResolvedValueOnce(Response.json({ ...pending(), status: 'error', error: secret }));
    const sessions = new DeviceAuthSessions(fetcher);
    await sessions.run('owner', 'POST', config);
    const result = await sessions.run('owner', 'GET', config);
    expect(result).toEqual({ status: 'error', error: expect.any(String) });
    expect(JSON.stringify(result)).not.toContain(secret);
    expect(JSON.stringify(result)).not.toContain('ABCD-EFGH');
    expect(JSON.stringify(result)).not.toContain(backendState);
  });

  it('handles a missing backend flow without polling it indefinitely', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending()))
      .mockResolvedValueOnce(new Response(secret, { status: 404 }));
    const sessions = new DeviceAuthSessions(fetcher);
    await sessions.run('owner', 'POST', config);
    const result = await sessions.run('owner', 'GET', config);
    expect(result).toMatchObject({ status: 'error' });
    expect(JSON.stringify(result)).not.toContain(secret);
    expect(await sessions.run('owner', 'GET', config)).toEqual(result);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});
