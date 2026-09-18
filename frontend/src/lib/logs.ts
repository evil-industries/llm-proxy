export const LOG_PAGE_SIZE = 200;
export const LOG_FETCH_SIZE = 1000;
export const LOG_POLL_INTERVAL = 5000;
export const LOG_LEVELS = ['error', 'warn', 'info', 'debug', 'trace', 'other'] as const;
export type LogLevel = (typeof LOG_LEVELS)[number];
export interface LogEntry {
  id: number;
  text: string;
  level: LogLevel;
  reset?: boolean;
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
export function filterLogs(entries: LogEntry[], query: string, level: string): LogEntry[] {
  const needle = query.toLowerCase();
  return entries.filter(
    (entry) =>
      (level === 'all' || entry.level === level) && entry.text.toLowerCase().includes(needle)
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
