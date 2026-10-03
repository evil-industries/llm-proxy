import { expect, it } from 'vitest';
import { formatDateTime, formatTime, formatRelativeTime, localizedLogText } from './datetime';
it('formats relative reset and observation times from the supplied clock', () => {
  const now = Date.parse('2026-10-03T12:00:00Z');
  expect(formatRelativeTime(now + 7200000, now)).toBe('in 2 Std.');
  expect(formatRelativeTime(now - 300000, now)).toBe('vor 5 Min.');
  expect(formatRelativeTime(now, now)).toBe('jetzt');
  expect(formatRelativeTime(now + 3 * 86400000, now)).toBe('in 3 Tagen');
  expect(formatRelativeTime(NaN, now)).toBe('—');
});
it('uses German dates and a 24-hour clock including midnight', () => {
  const date = new Date(2026, 9, 3, 0, 7, 8);
  expect(formatDateTime(date)).toBe('03.10.2026, 00:07:08');
  expect(formatTime(date)).toBe('00:07');
  expect(formatTime(new Date(2026, 9, 3, 17, 30))).toBe('17:30');
});
it('localizes absolute log times without timezone annotations', () => {
  const instant = Date.parse('2026-10-03T12:30:45+02:00');
  expect(localizedLogText('[2026-10-03 12:30:45] [info] msg', instant)).toBe(
    `[${formatDateTime(instant)}] [info] msg`
  );
  expect(localizedLogText('[2026-10-03 12:30:45+02:00] msg')).toBe(
    `[${formatDateTime(instant)}] msg`
  );
  expect(localizedLogText('[2026-10-03 12:30:45] msg')).toBe('[—] msg');
});
