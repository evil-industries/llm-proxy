import type { RequestHandler } from './$types';
import { relayManagement, safeJSON } from '$lib/server/relay';

const relay: RequestHandler = ({ request, params, locals }) => {
  if (!locals.managementConfiguration) return safeJSON({ error: locals.configurationError }, 503);
  if (!locals.authenticated) return safeJSON({ error: 'Sign in to continue.' }, 401);
  return relayManagement(request, params.path ?? '', locals.managementConfiguration);
};

export const GET = relay;
export const POST = relay;
export const PUT = relay;
export const PATCH = relay;
export const DELETE = relay;
