import { deviceAuthSessions } from '$lib/server/device-auth';
import { dev } from '$app/environment';
import type { RequestHandler } from './$types';
import {
  authStore,
  cookieOptions,
  passwordMatches,
  sameOrigin,
  sessionCookieName
} from '$lib/server/auth';
import { boundedBody, safeJSON } from '$lib/server/relay';

export const GET: RequestHandler = ({ locals }) =>
  safeJSON({
    authenticated: locals.authenticated,
    configured: Boolean(locals.managementConfiguration)
  });

export const POST: RequestHandler = async ({ request, url, locals, cookies, getClientAddress }) => {
  if (!sameOrigin(request, url))
    return safeJSON({ error: 'Request origin was not accepted.' }, 403);
  if (!locals.managementConfiguration)
    return safeJSON({ code: 'not_configured', error: locals.configurationError }, 503);
  if (!/^application\/json(?:\s*;|$)/i.test(request.headers.get('content-type') ?? ''))
    return safeJSON({ error: 'Send the password as JSON.' }, 415);
  const address = getClientAddress();
  if (!authStore.permitLogin(address))
    return safeJSON({ error: 'Too many sign-in attempts. Try again in 15 minutes.' }, 429);
  let password: unknown;
  try {
    const bytes = await boundedBody(request.body, 4096);
    const body: unknown = JSON.parse(new TextDecoder().decode(bytes));
    password =
      body && typeof body === 'object' ? (body as Record<string, unknown>).password : undefined;
  } catch {
    return safeJSON({ error: 'Send a valid password.' }, 400);
  }
  if (typeof password !== 'string' || !password || password.length > 1024)
    return safeJSON({ error: 'Send a valid password.' }, 400);
  let matches: boolean | null;
  try {
    matches = await passwordMatches(password, locals.managementConfiguration.passwordHash);
  } catch {
    return safeJSON(
      {
        code: 'temporarily_unavailable',
        error: 'Sign-in is temporarily unavailable. Try again shortly.'
      },
      503
    );
  }
  if (matches === null)
    return safeJSON(
      { code: 'temporarily_unavailable', error: 'Sign-in is busy. Try again shortly.' },
      503
    );
  if (!matches) return safeJSON({ error: 'Password was not accepted.' }, 401);
  const token = authStore.createSession(address);
  if (!token)
    return safeJSON(
      { code: 'temporarily_unavailable', error: 'The session limit was reached. Try again later.' },
      503
    );
  const production = !dev;
  const name = sessionCookieName(production);
  const previousIdentity = authStore.identity(cookies.get(name));
  authStore.revoke(cookies.get(name));
  void deviceAuthSessions.forget(previousIdentity, locals.managementConfiguration);
  cookies.set(name, token, cookieOptions(production));
  return safeJSON({ authenticated: true });
};

export const DELETE: RequestHandler = ({ request, url, cookies, locals }) => {
  if (!sameOrigin(request, url))
    return safeJSON({ error: 'Request origin was not accepted.' }, 403);
  const name = sessionCookieName(!dev);
  const previousIdentity = authStore.identity(cookies.get(name));
  authStore.revoke(cookies.get(name));
  void deviceAuthSessions.forget(previousIdentity, locals.managementConfiguration);
  cookies.delete(name, { path: '/' });
  return safeJSON({ authenticated: false });
};
