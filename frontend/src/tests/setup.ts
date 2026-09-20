import '../app.css';
import 'vitest-browser-svelte';
import { afterEach, beforeEach, vi } from 'vitest';

import { mockRealtime } from './realtime-fixture';
beforeEach(mockRealtime);

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
