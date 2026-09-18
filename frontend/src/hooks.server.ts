import { env } from '$env/dynamic/private';
import { dev } from '$app/environment';
import { redirect, type Handle } from '@sveltejs/kit';
import { authStore, sessionCookieName } from '$lib/server/auth';
import { ConfigurationError, loadConfiguration } from '$lib/server/config';
import { safeJSON } from '$lib/server/relay';

export const handle: Handle = async ({ event, resolve }) => {
  event.locals.managementConfiguration = null;
  event.locals.configurationError = null;
  try {
    event.locals.managementConfiguration = loadConfiguration(env, !dev);
  } catch (error) {
    event.locals.configurationError =
      error instanceof ConfigurationError
        ? error.message
        : 'Management server configuration is unavailable.';
  }
  const production = !dev;
  if (
    event.locals.managementConfiguration?.origin &&
    event.url.origin !== event.locals.managementConfiguration.origin
  ) {
    return safeJSON({ error: 'Request origin does not match the configured public origin.' }, 403);
  }
  event.locals.sessionIdentity = event.locals.managementConfiguration
    ? authStore.identity(event.cookies.get(sessionCookieName(production)))
    : undefined;
  event.locals.authenticated = Boolean(event.locals.sessionIdentity);
  const path = event.url.pathname;
  if (path !== '/login' && path !== '/api/session') {
    if (path.startsWith('/v0/'))
      return safeJSON({ error: 'Direct management access is unavailable.' }, 404);
    if (!event.locals.managementConfiguration)
      return safeJSON({ error: event.locals.configurationError }, 503);
    if (!event.locals.authenticated) {
      if (path.startsWith('/api/')) return safeJSON({ error: 'Sign in to continue.' }, 401);
      redirect(303, '/login');
    }
  }
  const response = await resolve(event);
  response.headers.set('Cache-Control', 'no-store');
  response.headers.set('X-Content-Type-Options', 'nosniff');
  response.headers.set('Referrer-Policy', 'same-origin');
  response.headers.set('X-Frame-Options', 'DENY');
  return response;
};
