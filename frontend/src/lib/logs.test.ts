import { describe, expect, it } from 'vitest';
import { fileSize, filterLogs, logLevel, requestIDMatches } from './logs';
describe('log inspection helpers', () => {
  it.each([
    ['[2026-09-17 10:30:21] [info ] request', 'info'],
    ['time="now" level=warning msg="slow"', 'warn'],
    ['2026-09-17 ERROR failure', 'error'],
    ['panic: unavailable', 'error'],
    ['continuation text', 'other']
  ])('classifies %s', (text, expected) => expect(logLevel(text)).toBe(expected));
  it('combines case-insensitive literal search and severity without changing history', () => {
    const entries = [
      { id: 1, text: '[info] a[b]', level: 'info' as const },
      { id: 2, text: 'ERROR A[B]', level: 'error' as const }
    ];
    expect(filterLogs(entries, 'a[b]', 'error')).toEqual([entries[1]]);
    expect(entries).toHaveLength(2);
  });
  it('matches exact request ID suffixes, not arbitrary substrings', () => {
    expect(requestIDMatches('error-2026-abc.log', ' abc ')).toBe(true);
    expect(requestIDMatches('error-2026-abc.log', 'ab')).toBe(false);
    expect(requestIDMatches('error-2026-abc.log', '')).toBe(false);
  });
  it('formats file sizes without assuming all logs are tiny', () => {
    expect(fileSize(32)).toBe('32 B');
    expect(fileSize(1024)).toBe('1.0 KiB');
    expect(fileSize(1048576)).toBe('1.0 MiB');
  });
});

it('filters exact thread IDs and correlates earlier request lines without mixing ambiguous requests', () => {
  const entries = [
    '[2026-10-03 12:00:00Z] [req1] [info ] incoming',
    '[2026-10-03 12:00:00Z] [req1] [info ] routed thread_id="thread-a" account_id="account-1"',
    '[2026-10-03 12:00:00Z] [req2] [info ] routed thread_id="thread-ab"',
    '[2026-10-03 12:00:00Z] [--------] [info ] unrelated'
  ].map((text, id) => ({ id, text, level: 'info' as const }));
  expect(filterLogs(entries, '', 'all', 'thread-a').map((entry) => entry.id)).toEqual([0, 1]);
  expect(filterLogs(entries, '', 'all', 'thread')).toEqual([]);
  expect(filterLogs(entries, 'incoming', 'all', 'thread-a').map((entry) => entry.id)).toEqual([0]);
  entries.push({
    id: 4,
    text: '[2026-10-03 12:00:00Z] [req1] [info ] thread_id="other"',
    level: 'info'
  });
  expect(filterLogs(entries, '', 'all', 'thread-a').map((entry) => entry.id)).toEqual([1]);
});

it('searches displayed German dates while retaining raw lines for export', () => {
  const entries = [
    {
      id: 0,
      text: '[2026-10-03 17:30:00] [info] ready',
      timestamp: new Date(2026, 9, 3, 17, 30).getTime(),
      level: 'info' as const
    }
  ];
  expect(filterLogs(entries, '03.10.2026', 'all')).toEqual(entries);
  expect(filterLogs(entries, '2026-10-03', 'all')).toEqual(entries);
});
