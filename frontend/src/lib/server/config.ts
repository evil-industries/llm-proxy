import { validPasswordHash } from './auth';

export interface ServerConfiguration {
  passwordHash: string;
  oidc?: OIDCConfiguration;
  managementURL: string;
  managementKey: string;
  origin?: string;
}

export interface OIDCConfiguration {
  issuer: string;
  clientId: string;
  clientSecret: string;
  allowedUsers: string[];
  redirectURI: string;
}

export class ConfigurationError extends Error {}

export function loadConfiguration(
  env: Record<string, string | undefined>,
  production = false
): ServerConfiguration {
  let origin: string | undefined;
  const mode = env.AUTH_MODE?.trim() || 'password';
  if (!['password', 'oidc'].includes(mode))
    throw new ConfigurationError('AUTH_MODE must be password or oidc.');
  if (production || mode === 'oidc') {
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
  const required = ['PRIVATE_MANAGEMENT_URL', 'PRIVATE_MANAGEMENT_KEY'];
  required.push(
    ...(mode === 'oidc'
      ? ['OIDC_ISSUER', 'OIDC_CLIENT_ID', 'OIDC_CLIENT_SECRET', 'OIDC_ALLOWED_USERS']
      : ['AUTH_PASSWORD_HASH'])
  );
  for (const name of required) {
    if (!env[name]?.trim())
      throw new ConfigurationError(`Management is not configured. Set ${name} on the server.`);
  }
  const passwordHash = mode === 'password' ? env.AUTH_PASSWORD_HASH!.trim() : '';
  if (mode === 'password' && !validPasswordHash(passwordHash))
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
  let oidc: OIDCConfiguration | undefined;
  if (mode === 'oidc') {
    let issuer: URL;
    try {
      issuer = new URL(env.OIDC_ISSUER!.trim());
      if (
        issuer.protocol !== 'https:' ||
        issuer.username ||
        issuer.password ||
        issuer.search ||
        issuer.hash
      )
        throw new Error();
    } catch {
      throw new ConfigurationError(
        'OIDC_ISSUER must be an HTTPS URL without credentials, query, or fragment.'
      );
    }
    const allowedUsers = env
      .OIDC_ALLOWED_USERS!.split(',')
      .map((user) => user.trim())
      .filter(Boolean);
    if (!allowedUsers.length)
      throw new ConfigurationError(
        'OIDC_ALLOWED_USERS must contain at least one permitted username.'
      );
    oidc = {
      issuer: env.OIDC_ISSUER!.trim(),
      clientId: env.OIDC_CLIENT_ID!.trim(),
      clientSecret: env.OIDC_CLIENT_SECRET!.trim(),
      allowedUsers,
      redirectURI: `${origin}/auth/oidc/callback`
    };
  }
  return {
    passwordHash,
    oidc,
    managementURL: url.toString().replace(/\/+$/, ''),
    managementKey,
    origin
  };
}
