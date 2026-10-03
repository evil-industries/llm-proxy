/** German date order and 24-hour time in the viewer's local timezone. */
export function formatDateTime(value: string | number | Date): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '—';
  return new Intl.DateTimeFormat('de-DE', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23'
  }).format(date);
}
export function formatTime(value: string | number | Date = new Date()): string {
  return new Intl.DateTimeFormat('de-DE', {
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23'
  }).format(new Date(value));
}

const relativeFormatter = new Intl.RelativeTimeFormat('de-DE', { style: 'short', numeric: 'auto' });
export function formatRelativeTime(value: number, now: number): string {
  if (!Number.isFinite(value) || !Number.isFinite(now)) return '—';
  const seconds = (value - now) / 1000;
  const distance = Math.abs(seconds);
  const [unit, divisor]: [Intl.RelativeTimeFormatUnit, number] =
    distance < 60
      ? ['second', 1]
      : distance < 3600
        ? ['minute', 60]
        : distance < 86400
          ? ['hour', 3600]
          : ['day', 86400];
  return relativeFormatter.format(Math.round(seconds / divisor), unit);
}

/** The server resolves legacy wall times; every displayed timestamp uses the viewer's zone. */
export function localizedLogText(text: string, timestamp?: number): string {
  return text.replace(
    /^\[(\d{4})-(\d{2})-(\d{2})[ T](\d{2}:\d{2}:\d{2})(Z|[+-]\d{2}:\d{2})?\]/,
    (_, year, month, day, time, zone) => {
      const instant =
        timestamp ?? (zone ? Date.parse(`${year}-${month}-${day}T${time}${zone}`) : NaN);
      return `[${formatDateTime(instant)}]`;
    }
  );
}
