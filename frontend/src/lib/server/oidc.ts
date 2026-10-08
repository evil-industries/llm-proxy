import { createHash, randomBytes } from 'node:crypto';
import * as client from 'openid-client';
import type { OIDCConfiguration } from './config';

export const OIDC_TRANSACTION_SECONDS = 10 * 60;
export const oidcCookieName = (production: boolean) =>
  production ? '__Host-management_oidc' : 'management_oidc';

interface Transaction {
  state: string;
  nonce: string;
  verifier: string;
  configuration: string;
  expires: number;
}

const digest = (value: string) => createHash('sha256').update(value).digest('hex');
const configurationKey = (configuration: OIDCConfiguration) =>
  digest(JSON.stringify(configuration));

/** The browser cookie binds each one-time response to its initiating browser. */
export class OIDCTransactions {
  private entries = new Map<string, Transaction>();
  constructor(private readonly now: () => number = Date.now) {}

  private prune() {
    for (const [key, entry] of this.entries)
      if (entry.expires <= this.now()) this.entries.delete(key);
  }

  create(configuration: OIDCConfiguration) {
    this.prune();
    if (this.entries.size >= 1000) return null;
    const token = randomBytes(32).toString('base64url');
    const transaction: Transaction = {
      state: client.randomState(),
      nonce: client.randomNonce(),
      verifier: client.randomPKCECodeVerifier(),
      configuration: configurationKey(configuration),
      expires: this.now() + OIDC_TRANSACTION_SECONDS * 1000
    };
    this.entries.set(digest(token), transaction);
    return { token, transaction };
  }

  consume(token: string | undefined, configuration: OIDCConfiguration): Transaction | undefined {
    this.prune();
    if (!token || !/^[A-Za-z0-9_-]{43}$/.test(token)) return;
    const key = digest(token);
    const transaction = this.entries.get(key);
    this.entries.delete(key);
    if (transaction?.configuration === configurationKey(configuration)) return transaction;
  }
}

export const oidcTransactions = new OIDCTransactions();
let cached: { key: string; value: Promise<client.Configuration> } | undefined;

export function oidcClient(configuration: OIDCConfiguration): Promise<client.Configuration> {
  const key = configurationKey(configuration);
  if (cached?.key === key) return cached.value;
  const value = client
    .discovery(
      new URL(configuration.issuer),
      configuration.clientId,
      { id_token_signed_response_alg: 'RS256' },
      client.ClientSecretBasic(configuration.clientSecret),
      { timeout: 10, execute: [client.enableNonRepudiationChecks] }
    )
    .catch((error: unknown) => {
      if (cached?.key === key) cached = undefined;
      throw error;
    });
  cached = { key, value };
  return value;
}

export async function authorizationURL(configuration: OIDCConfiguration, transaction: Transaction) {
  const oidc = await oidcClient(configuration);
  return client.buildAuthorizationUrl(oidc, {
    redirect_uri: configuration.redirectURI,
    scope: 'openid profile',
    response_mode: 'query',
    code_challenge_method: 'S256',
    code_challenge: await client.calculatePKCECodeChallenge(transaction.verifier),
    state: transaction.state,
    nonce: transaction.nonce
  });
}

export async function authorizedUser(
  configuration: OIDCConfiguration,
  transaction: Transaction,
  callback: URL
): Promise<boolean> {
  const oidc = await oidcClient(configuration);
  const tokens = await client.authorizationCodeGrant(oidc, callback, {
    pkceCodeVerifier: transaction.verifier,
    expectedState: transaction.state,
    expectedNonce: transaction.nonce,
    idTokenExpected: true
  });
  const claims = tokens.claims();
  if (!claims?.sub) return false;
  const user = await client.fetchUserInfo(oidc, tokens.access_token, claims.sub);
  return (
    typeof user.preferred_username === 'string' &&
    configuration.allowedUsers.includes(user.preferred_username)
  );
}

export function oidcRedirect(location: string): Response {
  return new Response(null, {
    status: 303,
    headers: {
      Location: location,
      'Cache-Control': 'no-store',
      'Referrer-Policy': 'no-referrer'
    }
  });
}
