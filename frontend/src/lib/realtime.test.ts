import { afterEach, expect, it, vi } from 'vitest';
import { subscribeChanges } from './realtime';
import { MockEventSource, mockRealtime } from '../tests/realtime-fixture';
afterEach(() => vi.unstubAllGlobals());
it('shares one stream, resyncs on reconnect and closes after the last subscriber leaves', () => {
  mockRealtime();
  const first = vi.fn(),
    second = vi.fn();
  const stopFirst = subscribeChanges(first);
  const stopSecond = subscribeChanges(second);
  expect(MockEventSource.instances).toHaveLength(1);
  MockEventSource.emit(1);
  expect(first).toHaveBeenLastCalledWith(1);
  expect(second).toHaveBeenLastCalledWith(1);
  MockEventSource.reconnect();
  expect(first).toHaveBeenLastCalledWith(31);
  stopFirst();
  expect(MockEventSource.instances[0].closed).toBe(false);
  MockEventSource.emit(4);
  expect(first).toHaveBeenCalledTimes(2);
  expect(second).toHaveBeenLastCalledWith(4);
  stopSecond();
  expect(MockEventSource.instances[0].closed).toBe(true);
});
it('reports disconnects without scheduling data polling and stops reconnecting when the session ends', () => {
  mockRealtime();
  const listener = vi.fn(),
    status = vi.fn();
  const stop = subscribeChanges(listener, status);
  const source = MockEventSource.instances[0];
  source.onerror?.();
  expect(status).toHaveBeenLastCalledWith('reconnecting');
  expect(listener).not.toHaveBeenCalled();
  source.dispatchEvent(new Event('session-ended'));
  expect(source.closed).toBe(true);
  expect(listener).toHaveBeenLastCalledWith(31);
  stop();
});
