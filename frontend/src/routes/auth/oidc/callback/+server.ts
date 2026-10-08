import { dev } from '$app/environment';
import type { RequestHandler } from './$types';
import { authStore, cookieOptions, sessionCookieName } from '$lib/server/auth';
import { deviceAuthSessions } from '$lib/server/device-auth';
import { oidcRedirect, authorizedUser, oidcCookieName, oidcTransactions } from '$lib/server/oidc';
import { safeJSON } from '$lib/server/relay';

export const GET: RequestHandler = async ({ url, locals, cookies, getClientAddress }) => {
  const configuration = locals.managementConfiguration?.oidc;
  if (!configuration) return safeJSON({ error: 'Authelia sign-in is not configured.' }, 503);
  const name = oidcCookieName(!dev);
  const transaction = oidcTransactions.consume(cookies.get(name), configuration);
  cookies.delete(name, { path: '/' });
  if (!transaction) return oidcRedirect('/login?error=oidc');
  let permitted = false;
  try {
    permitted = await authorizedUser(configuration, transaction, url);
  } catch {
    // Provider responses can contain secrets. Return only a fixed error message.
  }
  if (!permitted) return oidcRedirect('/login?error=oidc');
  const token = authStore.createSession(getClientAddress());
  if (!token) return safeJSON({ error: 'The session limit was reached. Try again later.' }, 503);
  const sessionName = sessionCookieName(!dev);
  const previous = cookies.get(sessionName);
  const identity = authStore.identity(previous);
  authStore.revoke(previous);
  void deviceAuthSessions.forget(identity, locals.managementConfiguration);
  cookies.set(sessionName, token, cookieOptions(!dev));
  return oidcRedirect('/');
};
