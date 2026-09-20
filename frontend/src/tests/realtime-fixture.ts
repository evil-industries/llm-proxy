import { vi } from 'vitest';

export class MockEventSource extends EventTarget {
  static instances: MockEventSource[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(public url: string) {
    super();
    MockEventSource.instances.push(this);
  }
  close() {
    this.closed = true;
  }
  static emit(topics: number) {
    for (const source of this.instances)
      if (!source.closed) source.onmessage?.(new MessageEvent('message', { data: String(topics) }));
  }
  static reconnect() {
    for (const source of this.instances) if (!source.closed) source.onopen?.();
  }
}
export function mockRealtime() {
  MockEventSource.instances = [];
  vi.stubGlobal('EventSource', MockEventSource);
}
