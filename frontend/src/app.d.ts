import type { ServerConfiguration } from './lib/server/config';
declare global {
  namespace App {
    interface Locals {
      authenticated: boolean;
      sessionIdentity?: string;
      managementConfiguration: ServerConfiguration | null;
      configurationError: string | null;
    }
  }
}
export {};
