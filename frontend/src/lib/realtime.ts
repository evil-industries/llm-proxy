// One authenticated push connection shared by all mounted console views.
export const Changes = { accounts: 1, config: 2, logs: 4, requestLogs: 8, deviceAuth: 16 } as const;
const ALL = 31;
type Listener = (topics: number) => void;
export type LiveStatus = 'connecting' | 'connected' | 'reconnecting';
const listeners = new Map<Listener, ((status: LiveStatus) => void) | undefined>();
let source: EventSource | undefined;

export function subscribeChanges(
  listener: Listener,
  status?: (value: LiveStatus) => void
): () => void {
  listeners.set(listener, status);
  if (!source) {
    source = new EventSource('/api/management/events');
    source.onerror = () => {
      for (const status of listeners.values()) status?.('reconnecting');
    };
    source.onopen = () => {
      for (const status of listeners.values()) status?.('connected');
      // Recover changes missed while disconnected using fresh snapshots/cursors.
      for (const notify of listeners.keys()) notify(ALL);
    };
    source.onmessage = (event) => {
      const topics = Number(event.data);
      if (!Number.isInteger(topics) || topics < 1 || topics > ALL) return;
      for (const notify of listeners.keys()) notify(topics);
    };
    source.addEventListener('session-ended', () => {
      source?.close();
      // Let the regular authenticated read clear session data and render sign-in.
      for (const notify of listeners.keys()) notify(ALL);
    });
  }
  return () => {
    listeners.delete(listener);
    if (!listeners.size) {
      source?.close();
      source = undefined;
    }
  };
}
