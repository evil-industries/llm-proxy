import type { RequestHandler } from './$types';
import { deviceAuthSessions, DeviceAuthError } from '$lib/server/device-auth';
import { sameOrigin } from '$lib/server/auth';
import { safeJSON } from '$lib/server/relay';

const handle: RequestHandler = async ({ request, url, locals }) => {
  if (!locals.authenticated || !locals.sessionIdentity)
    return safeJSON({ error: 'Sign in to continue.' }, 401);
  if (!locals.managementConfiguration)
    return safeJSON({ error: 'Management is not configured.' }, 503);
  if (url.search) return safeJSON({ error: 'Device login is bound to your console session.' }, 400);
  if (request.method !== 'GET' && !sameOrigin(request, url))
    return safeJSON({ error: 'Request origin was not accepted.' }, 403);
  try {
    const result = await deviceAuthSessions.run(
      locals.sessionIdentity,
      request.method as 'GET' | 'POST' | 'DELETE',
      locals.managementConfiguration
    );
    return safeJSON(result);
  } catch (error) {
    return error instanceof DeviceAuthError
      ? safeJSON({ error: error.message }, error.status)
      : safeJSON({ error: 'Device login could not be completed. Try again.' }, 502);
  }
};

export const GET = handle;
export const POST = handle;
export const DELETE = handle;
