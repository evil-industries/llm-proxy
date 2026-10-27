// Keep the password generator compatible with the Go console verifier.
export const SCRYPT_OPTIONS = Object.freeze({ N: 131072, r: 8, p: 1, maxmem: 256 * 1024 * 1024 });
export const SCRYPT_PREFIX = 'scrypt$131072$8$1';
