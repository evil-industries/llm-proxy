import '../app.css';
import 'vitest-browser-svelte';
import { afterEach, vi } from 'vitest';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

declare module 'vitest/browser' {
  interface BrowserCommands {
    auditAccessibility: () => Promise<
      Array<{ id: string; impact: string | null; targets: unknown[] }>
    >;
  }
}
