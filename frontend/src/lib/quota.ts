import type { AuthFile } from './api/types';

export interface QuotaWindow {
  key: string;
  label: string;
  remaining: number;
  observed: number;
  reset?: number;
}
export interface AccountQuota {
  windows: QuotaWindow[];
  credits?: string;
  resetCredits?: number;
  observed?: number;
}
export const QUOTA_MAX_AGE = 15 * 60 * 1000;
const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const numeric = (value: unknown): number | undefined => {
  if (typeof value !== 'string' || !value.trim()) return;
  const n = Number(value);
  return Number.isFinite(n) ? n : undefined;
};
const title = (value: string) => value.replace(/[-_]/g, ' ');
function periodLabel(window: string, minutes: number | undefined): string {
  if (minutes !== undefined && minutes > 0) {
    if (minutes % 1440 === 0) return `${minutes / 1440}-day window`;
    if (minutes % 60 === 0) return `${minutes / 60}-hour window`;
    return `${minutes}-minute window`;
  }
  return (
    (
      {
        '5h': '5-hour window',
        '7d': '7-day window',
        primary: 'Primary window',
        secondary: 'Secondary window'
      } as Record<string, string>
    )[window] ?? title(window)
  );
}

/** Decode only provider-reported allowances, never routing cooldowns or request counts. */
export function accountQuota(file: AuthFile): AccountQuota {
  const provider = (file.provider || file.type || '').toLowerCase();
  if (provider !== 'codex' && provider !== 'claude') return { windows: [] };
  const quotas: unknown[] = [
    file.quota,
    ...Object.values(record(file.model_quotas) ? file.model_quotas : {})
  ];
  const latest = new Map<string, QuotaWindow>();
  const result: AccountQuota = { windows: [] };
  for (const quota of quotas) {
    if (!record(quota) || !record(quota.signals) || typeof quota.observed_at !== 'string') continue;
    const observed = Date.parse(quota.observed_at);
    if (!Number.isFinite(observed)) continue;
    const signals: Record<string, string> = {};
    for (const [key, value] of Object.entries(quota.signals)) {
      if (typeof value === 'string') signals[key.toLowerCase()] = value.trim();
    }
    if (provider === 'codex' && (result.observed === undefined || observed > result.observed)) {
      result.observed = observed;
      const balance = numeric(signals['x-codex-credits-balance']);
      result.credits =
        signals['x-codex-credits-unlimited'] === 'true'
          ? 'Unlimited credits'
          : balance !== undefined && balance >= 0
            ? `${balance.toLocaleString()} credits`
            : undefined;
      const resets = numeric(signals['x-codex-rate-limit-reset-credits-available-count']);
      result.resetCredits =
        resets !== undefined && Number.isInteger(resets) && resets >= 0 ? resets : undefined;
    }
    for (const [name, value] of Object.entries(signals)) {
      const match =
        provider === 'codex'
          ? /^x-codex-(.+)-used-percent$/.exec(name)
          : /^anthropic-ratelimit-unified-(.+)-utilization$/.exec(name);
      if (!match) continue;
      const used = numeric(value);
      const scale = provider === 'codex' ? 100 : 1;
      if (used === undefined || used < 0 || used > scale) continue;
      const window = match[1];
      const prefix = name.replace(provider === 'codex' ? /-used-percent$/ : /-utilization$/, '');
      const absoluteKey = `${prefix}-${provider === 'codex' ? 'reset-at' : 'reset'}`;
      let reset: number | undefined;
      if (absoluteKey in signals) {
        const seconds = numeric(signals[absoluteKey]);
        if (seconds !== undefined && seconds > 0) reset = seconds * 1000;
      } else if (provider === 'codex') {
        const seconds = numeric(signals[`${prefix}-reset-after-seconds`]);
        if (seconds !== undefined && seconds >= 0) reset = observed + seconds * 1000;
      }
      if (reset !== undefined && !Number.isFinite(new Date(reset).getTime())) reset = undefined;
      let key = window;
      let label = periodLabel(window, numeric(signals[`${prefix}-window-minutes`]));
      if (provider === 'codex') {
        const parts = /^(.*)-(primary|secondary)$/.exec(window);
        const period = parts?.[2] ?? window;
        const pool =
          parts?.[1] ??
          (period === 'primary' || period === 'secondary'
            ? signals['x-codex-active-limit']?.replace(/^codex_/, '')
            : undefined);
        const poolName = pool ? signals[`x-codex-${pool}-limit-name`] || pool : undefined;
        key = poolName ? `${poolName.toLowerCase()}:${period}` : window;
        label = `${poolName ? `${title(poolName)} · ` : ''}${periodLabel(period, numeric(signals[`${prefix}-window-minutes`]))}`;
      }
      const item = { key, label, remaining: 100 * (1 - used / scale), observed, reset };
      const previous = latest.get(key);
      if (
        !previous ||
        observed > previous.observed ||
        (observed === previous.observed && item.remaining < previous.remaining)
      )
        latest.set(key, item);
    }
  }
  result.windows = [...latest.values()].sort((a, b) => a.key.localeCompare(b.key));
  return result;
}

export function quotaExpired(window: QuotaWindow, now: number): boolean {
  return window.reset !== undefined && window.reset <= now;
}
export function quotaStale(window: QuotaWindow, now: number): boolean {
  return (
    window.observed > now || now - window.observed >= QUOTA_MAX_AGE || quotaExpired(window, now)
  );
}
export function resetLabel(reset: number | undefined): string {
  return reset === undefined
    ? 'Reset time unavailable'
    : new Date(reset).toLocaleString(undefined, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        timeZoneName: 'short'
      });
}
