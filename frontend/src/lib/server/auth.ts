import { createHash, randomBytes, scrypt as scryptCallback, timingSafeEqual } from 'node:crypto';
import { SCRYPT_OPTIONS } from './password-params.js';

export const SESSION_SECONDS = 8 * 60 * 60;
const LOGIN_WINDOW = 15 * 60 * 1000;
const MAX_ATTEMPTS = 5;
const MAX_ENTRIES = 1000;
const HASH_PATTERN = /^scrypt\$131072\$8\$1\$([a-f0-9]{32})\$([a-f0-9]{128})$/;
let activeVerifications = 0;

export function validPasswordHash(hash: string): boolean {
  return HASH_PATTERN.test(hash);
}

export async function passwordMatches(password: string, encoded: string): Promise<boolean | null> {
  const match = HASH_PATTERN.exec(encoded);
  if (!match || password.length > 1024) return false;
  if (activeVerifications >= 2) return null;
  activeVerifications++;
  try {
    const actual = await new Promise<Buffer>((resolve, reject) => {
      scryptCallback(
        password,
        Buffer.from(match[1], 'hex'),
        64,
        SCRYPT_OPTIONS,
        (error, derived) => {
          if (error) reject(error);
          else resolve(derived);
        }
      );
    });
    return timingSafeEqual(actual, Buffer.from(match[2], 'hex'));
  } finally {
    activeVerifications--;
  }
}

const digest = (value: string) => createHash('sha256').update(value).digest('hex');

/** Single-process session storage. Expiration is checked on every access; no timers. */
export class AuthStore {
  private sessions = new Map<string, number>();
  private attempts = new Map<string, { count: number; expires: number }>();
  constructor(private readonly now: () => number = Date.now) {}

  private prune() {
    const now = this.now();
    for (const [token, expires] of this.sessions) if (expires <= now) this.sessions.delete(token);
    for (const [address, attempt] of this.attempts)
      if (attempt.expires <= now) this.attempts.delete(address);
  }

  /** Reserve an attempt before computing scrypt, including concurrent requests. */
  permitLogin(address: string): boolean {
    this.prune();
    const key = digest(address);
    const attempt = this.attempts.get(key);
    if (attempt) {
      if (attempt.count >= MAX_ATTEMPTS) return false;
      attempt.count++;
    } else {
      if (this.attempts.size >= MAX_ENTRIES) return false;
      this.attempts.set(key, { count: 1, expires: this.now() + LOGIN_WINDOW });
    }
    return true;
  }

  createSession(address: string): string | null {
    this.prune();
    if (this.sessions.size >= MAX_ENTRIES) return null;
    const token = randomBytes(32).toString('base64url');
    this.sessions.set(digest(token), this.now() + SESSION_SECONDS * 1000);
    this.attempts.delete(digest(address));
    return token;
  }

  authenticated(token: string | undefined): boolean {
    this.prune();
    if (!token || !/^[A-Za-z0-9_-]{43}$/.test(token)) return false;
    return this.sessions.has(digest(token));
  }

  identity(token: string | undefined): string | undefined {
    return this.authenticated(token) ? digest(token!) : undefined;
  }

  revoke(token: string | undefined) {
    if (token) this.sessions.delete(digest(token));
  }
}

export const authStore = new AuthStore();
export const sessionCookieName = (production: boolean) =>
  production ? '__Host-management_session' : 'management_session';
export const cookieOptions = (production: boolean) => ({
  path: '/',
  httpOnly: true,
  sameSite: 'lax' as const,
  secure: production,
  maxAge: SESSION_SECONDS
});

export function sameOrigin(request: Request, url: URL): boolean {
  return request.headers.get('origin') === url.origin;
}
