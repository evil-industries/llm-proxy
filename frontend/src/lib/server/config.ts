import { validPasswordHash } from './auth';

export interface ServerConfiguration {
  passwordHash: string;
  managementURL: string;
  managementKey: string;
  origin?: string;
}

export class ConfigurationError extends Error {}

export function loadConfiguration(
  env: Record<string, string | undefined>,
  production = false
): ServerConfiguration {
  let origin: string | undefined;
  if (production) {
    try {
      const url = new URL(env.ORIGIN ?? '');
      if (
        url.protocol !== 'https:' ||
        url.username ||
        url.password ||
        url.pathname !== '/' ||
        url.search ||
        url.hash
      )
        throw new Error();
      origin = url.origin;
    } catch {
      throw new ConfigurationError(
        'Set ORIGIN to the public HTTPS origin, for example https://management.example.com.'
      );
    }
  }
  for (const name of ['AUTH_PASSWORD_HASH', 'PRIVATE_MANAGEMENT_URL', 'PRIVATE_MANAGEMENT_KEY']) {
    if (!env[name]?.trim())
      throw new ConfigurationError(`Management is not configured. Set ${name} on the server.`);
  }
  const passwordHash = env.AUTH_PASSWORD_HASH!.trim();
  if (!validPasswordHash(passwordHash))
    throw new ConfigurationError(
      'AUTH_PASSWORD_HASH is invalid. Generate it with npm run auth:hash.'
    );
  let url: URL;
  try {
    url = new URL(env.PRIVATE_MANAGEMENT_URL!.trim());
  } catch {
    throw new ConfigurationError('PRIVATE_MANAGEMENT_URL must be an HTTP or HTTPS server URL.');
  }
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  ) {
    throw new ConfigurationError(
      'PRIVATE_MANAGEMENT_URL must be an HTTP or HTTPS URL without credentials, query, or fragment.'
    );
  }
  const path = url.pathname.replace(/\/+$/, '');
  url.pathname = path.endsWith('/v0/management') ? path : `${path}/v0/management`;
  const managementKey = env.PRIVATE_MANAGEMENT_KEY!.trim();
  if (/[\r\n]/.test(managementKey))
    throw new ConfigurationError('PRIVATE_MANAGEMENT_KEY contains invalid characters.');
  return { passwordHash, managementURL: url.toString().replace(/\/+$/, ''), managementKey, origin };
}
