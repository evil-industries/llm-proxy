import type { RequestHandler } from './$types';
import { relayEvents } from '$lib/server/events';
import { safeJSON } from '$lib/server/relay';

export const GET: RequestHandler = ({ request, locals, url }) => {
  if (!locals.managementConfiguration) return safeJSON({ error: locals.configurationError }, 503);
  if (!locals.sessionIdentity) return safeJSON({ error: 'Sign in to continue.' }, 401);
  if (url.search) return safeJSON({ error: 'Invalid management query.' }, 400);
  return relayEvents(request, locals.sessionIdentity, locals.managementConfiguration);
};
