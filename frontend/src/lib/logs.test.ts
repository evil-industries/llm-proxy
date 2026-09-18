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
