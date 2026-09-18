import { describe, expect, it } from 'vitest';
import { aggregateRequestBuckets } from './chart';

describe('aggregateRequestBuckets', () => {
  it('merges matching server intervals across credentials and retains zero buckets', () => {
    expect(
      aggregateRequestBuckets([
        {
          name: 'a',
          recent_requests: [
            { time: '09:00-09:10', success: 5, failed: 1 },
            { time: '09:10-09:20', success: 0, failed: 0 }
          ]
        },
        { name: 'b', recent_requests: [{ time: '09:00-09:10', success: 4, failed: 2 }] }
      ])
    ).toEqual([
      { id: '09:00-09:10:0', time: '09:00-09:10', success: 9, failed: 3, total: 12 },
      { id: '09:10-09:20:0', time: '09:10-09:20', success: 0, failed: 0, total: 0 }
    ]);
  });
  it('preserves server order through midnight instead of sorting clock labels', () => {
    expect(
      aggregateRequestBuckets([
        {
          name: 'a',
          recent_requests: [
            { time: '23:50-00:00', success: 1, failed: 0 },
            { time: '00:00-00:10', success: 2, failed: 0 }
          ]
        }
      ]).map((bucket) => bucket.time)
    ).toEqual(['23:50-00:00', '00:00-00:10']);
  });
  it('returns no data when the server does not provide request history', () => {
    expect(aggregateRequestBuckets([{ name: 'a', success: 500 }])).toEqual([]);
  });
  it('discards malformed labels and never graphs invalid counts', () => {
    expect(
      aggregateRequestBuckets([
        {
          name: 'a',
          recent_requests: [
            { time: '', success: 1, failed: 0 },
            { time: '09:00-09:10', success: -2, failed: NaN },
            { time: '09:10-09:20', success: 1.5, failed: 3 }
          ]
        }
      ])
    ).toEqual([
      { id: '09:00-09:10:0', time: '09:00-09:10', success: 0, failed: 0, total: 0 },
      { id: '09:10-09:20:0', time: '09:10-09:20', success: 0, failed: 3, total: 3 }
    ]);
  });
});

const fallbackLabel = (timestamp: number) => {
  const format = new Intl.DateTimeFormat('en-GB', {
    timeZone: 'Europe/Berlin',
    hour: '2-digit',
    minute: '2-digit'
  });
  return `${format.format(timestamp * 1000)}-${format.format((timestamp + 600) * 1000)}`;
};

it.each([true, false])(
  'preserves twenty intervals across DST fallback (timestamps: %s)',
  (withTimestamps) => {
    const newest = Date.parse('2026-10-25T02:10:00Z') / 1000;
    const history = Array.from({ length: 20 }, (_, index) => {
      const timestamp = newest - (19 - index) * 600;
      return {
        time: fallbackLabel(timestamp),
        ...(withTimestamps ? { timestamp } : {}),
        success: index + 1,
        failed: 0
      };
    });
    const buckets = aggregateRequestBuckets([
      { name: 'a', recent_requests: history },
      { name: 'b', recent_requests: history }
    ]);
    expect(buckets).toHaveLength(20);
    expect(new Set(buckets.map((bucket) => bucket.id)).size).toBe(20);
    expect(buckets.map((bucket) => bucket.time)).toEqual(history.map((bucket) => bucket.time));
    expect(buckets.map((bucket) => bucket.success)).toEqual(
      history.map((bucket) => bucket.success * 2)
    );
  }
);

it('aligns timestamps when credential snapshots straddle an interval boundary', () => {
  const early = Date.parse('2026-10-25T00:00:00Z') / 1000;
  const late = early + 3600;
  const buckets = aggregateRequestBuckets([
    {
      name: 'a',
      recent_requests: [{ time: '02:00-02:10', timestamp: late, success: 3, failed: 0 }]
    },
    {
      name: 'b',
      recent_requests: [
        { time: '02:00-02:10', timestamp: early, success: 1, failed: 0 },
        { time: '02:00-02:10', timestamp: late, success: 2, failed: 0 }
      ]
    }
  ]);
  expect(buckets.map(({ timestamp, success }) => ({ timestamp, success }))).toEqual([
    { timestamp: early, success: 1 },
    { timestamp: late, success: 5 }
  ]);
});
