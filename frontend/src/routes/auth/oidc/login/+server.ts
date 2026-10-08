import { dev } from '$app/environment';
import type { RequestHandler } from './$types';
import { authStore, cookieOptions, sameOrigin } from '$lib/server/auth';
import {
  oidcRedirect,
  authorizationURL,
  oidcCookieName,
  oidcTransactions,
  OIDC_TRANSACTION_SECONDS
} from '$lib/server/oidc';
import { safeJSON } from '$lib/server/relay';

export const POST: RequestHandler = async ({ request, url, locals, cookies, getClientAddress }) => {
  if (!sameOrigin(request, url))
    return safeJSON({ error: 'Request origin was not accepted.' }, 403);
  const configuration = locals.managementConfiguration?.oidc;
  if (!configuration) return safeJSON({ error: 'Authelia sign-in is not configured.' }, 503);
  if (!authStore.permitLogin(getClientAddress()))
    return safeJSON({ error: 'Too many sign-in attempts. Try again in 15 minutes.' }, 429);
  const name = oidcCookieName(!dev);
  oidcTransactions.consume(cookies.get(name), configuration);
  const entry = oidcTransactions.create(configuration);
  if (!entry) return safeJSON({ error: 'Sign-in is temporarily unavailable.' }, 503);
  let location: URL;
  try {
    location = await authorizationURL(configuration, entry.transaction);
  } catch {
    oidcTransactions.consume(entry.token, configuration);
    return oidcRedirect('/login?error=oidc');
  }
  cookies.set(name, entry.token, {
    ...cookieOptions(!dev),
    maxAge: OIDC_TRANSACTION_SECONDS
  });
  return oidcRedirect(location.href);
};
