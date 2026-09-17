import type { AuthFile } from './api';

export interface RequestBucket {
  id: string;
  time: string;
  timestamp?: number;
  success: number;
  failed: number;
  total: number;
}

/** Keep interval identity separate from display labels, which repeat during DST fallback. */
export function aggregateRequestBuckets(files: AuthFile[]): RequestBucket[] {
  const buckets = new Map<string, RequestBucket>();
  for (const file of files) {
    const occurrences = new Map<string, number>();
    for (const bucket of file.recent_requests ?? []) {
      if (typeof bucket.time !== 'string' || !bucket.time.trim()) continue;
      const success = validCount(bucket.success);
      const failed = validCount(bucket.failed);
      const occurrence = occurrences.get(bucket.time) ?? 0;
      occurrences.set(bucket.time, occurrence + 1);
      const timestamp = Number.isSafeInteger(bucket.timestamp) ? bucket.timestamp : undefined;
      // Older backends provide ordered labels only. Keep each repeated occurrence distinct.
      const id =
        timestamp === undefined ? `${bucket.time}:${occurrence}` : `timestamp:${timestamp}`;
      const existing = buckets.get(id) ?? {
        id,
        time: bucket.time,
        ...(timestamp === undefined ? {} : { timestamp }),
        success: 0,
        failed: 0,
        total: 0
      };
      existing.success += success;
      existing.failed += failed;
      existing.total += success + failed;
      buckets.set(id, existing);
    }
  }
  const result = [...buckets.values()];
  return result.every((bucket) => bucket.timestamp !== undefined)
    ? result.sort((a, b) => a.timestamp! - b.timestamp!)
    : result;
}

function validCount(value: number): number {
  return Number.isSafeInteger(value) && value >= 0 ? value : 0;
}
