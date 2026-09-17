import type { ServerConfiguration } from './lib/server/config';
declare global {
  namespace App {
    interface Locals {
      authenticated: boolean;
      managementConfiguration: ServerConfiguration | null;
      configurationError: string | null;
    }
  }
}
export {};
