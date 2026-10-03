import { localizedLogText } from './datetime';

export const LOG_PAGE_SIZE = 200;
export const LOG_FETCH_SIZE = 1000;
export const LOG_LEVELS = ['error', 'warn', 'info', 'debug', 'trace', 'other'] as const;
export type LogLevel = (typeof LOG_LEVELS)[number];
export interface LogEntry {
  id: number;
  text: string;
  level: LogLevel;
  reset?: boolean;
  timestamp?: number;
}

/** Recognize the level in standard logrus text, bracketed output and simple console logs. */
export function logLevel(text: string): LogLevel {
  const match = text.match(
    /(?:^|\s|\[)(?:level=)?(panic|fatal|error|warning|warn|info|debug|trace)(?:\]|\s|:|$)/i
  );
  const level = match?.[1]?.toLowerCase();
  if (level === 'fatal' || level === 'panic') return 'error';
  if (level === 'warning') return 'warn';
  return LOG_LEVELS.includes(level as LogLevel) ? (level as LogLevel) : 'other';
}
export function filterLogs(
  entries: LogEntry[],
  query: string,
  level: string,
  thread = '',
  contexts = correlateLogs(entries)
): LogEntry[] {
  const needle = query.toLowerCase();
  return entries.filter(
    (entry) =>
      (level === 'all' || entry.level === level) &&
      (entry.text.toLowerCase().includes(needle) ||
        localizedLogText(entry.text, entry.timestamp).toLowerCase().includes(needle)) &&
      (!thread.trim() || contexts.get(entry.id)?.thread === thread.trim())
  );
}
export function fileSize(size: number): string {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`;
  return `${(size / 1024 / 1024).toFixed(1)} MiB`;
}
export function requestIDMatches(name: string, id: string): boolean {
  return !!id.trim() && name.endsWith(`-${id.trim()}.log`);
}

export interface LogContext {
  thread?: string;
  account?: string;
  request?: string;
}
/** Read complete structured IDs; never use the truncated IDs in legacy debug messages. */
export function logContext(text: string): LogContext {
  const fields: Record<string, string> = {};
  for (const match of text.matchAll(/(?:^|\s)([\w]+)=("(?:\\.|[^"\\])*"|[^\s]+)/g)) {
    try {
      fields[match[1]] = match[2].startsWith('"') ? JSON.parse(match[2]) : match[2];
    } catch {
      /* Leave malformed structured fields out of correlation. */
    }
  }
  const request =
    fields.request_id ??
    text.match(/^\[[^\]]+\] \[([^\]]+)\] \[(?:info|debug|warn|error|trace|fatal|panic)\s*\]/)?.[1];
  return {
    thread: fields.thread_id,
    account: fields.account_id,
    request: request && request !== '--------' ? request : undefined
  };
}
/** Correlate request-scoped lines, even when the routing line arrived later in the batch. */
export function correlateLogs(entries: LogEntry[]): Map<number, LogContext> {
  const contexts = new Map(entries.map((entry) => [entry.id, logContext(entry.text)]));
  const requests = new Map<string, string>();
  const ambiguous = new Set<string>();
  for (const context of contexts.values()) {
    if (!context.request || !context.thread) continue;
    const prior = requests.get(context.request);
    if (prior && prior !== context.thread) ambiguous.add(context.request);
    requests.set(context.request, context.thread);
  }
  for (const context of contexts.values()) {
    if (!context.thread && context.request && !ambiguous.has(context.request)) {
      context.thread = requests.get(context.request);
    }
  }
  return contexts;
}

/** Separate the fixed log columns while keeping the original line available in details/export. */
export function logPresentation(
  text: string,
  timestamp?: number
): { timestamp: string; message: string } {
  const dated = /^\[\d{4}-\d{2}-\d{2}[ T]/.test(text);
  if (!dated) return { timestamp: '—', message: text };
  const localized = localizedLogText(text, timestamp);
  const header = localized.match(/^\[([^\]]+)\]\s*(.*)$/s);
  if (!header) return { timestamp: '—', message: text };
  let message = header[2].replace(
    /^(?:\[[^\]]+\]\s*)?\[(?:info|warn|error|debug|trace|fatal|panic)\s*\]\s*(?:\[[^\]]+:\d+\]\s*)?/i,
    ''
  );
  const context = logContext(text);
  if (context.thread || context.account) {
    message = message.replace(
      /\s+(thread_id|account_id)=("(?:\\.|[^"\\])*"|[^\s]+)/g,
      (field, key) => ((key === 'thread_id' ? context.thread : context.account) ? '' : field)
    );
  }
  return { timestamp: header[1], message };
}
