import type { AuthFile } from './api';
import { accountQuota, quotaStale, type QuotaWindow } from './quota';

export const isCodex = (file: AuthFile) =>
  (file.provider || file.type || '').trim().toLowerCase() === 'codex';
export function codexPlan(file: AuthFile): string | undefined {
  if (!isCodex(file)) return;
  const claims = file.id_token;
  const fallback =
    claims && typeof claims === 'object' && 'plan_type' in claims ? claims.plan_type : undefined;
  const plan = accountQuota(file).plan ?? fallback;
  if (typeof plan !== 'string' || !plan.trim()) return;
  return plan
    .trim()
    .replace(/[_-]/g, ' ')
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}
/** Additional model-specific pools must not block or inflate the main subscription pool. */
export function subscriptionWindows(file: AuthFile): QuotaWindow[] {
  return accountQuota(file).windows.filter(
    (window) => window.key === 'primary' || window.key === 'secondary'
  );
}
export function subscriptionState(file: AuthFile, now: number) {
  if (file.disabled) return 'disabled';
  if (file.unavailable || file.status === 'error') return 'unavailable';
  const windows = subscriptionWindows(file);
  if (windows.some((window) => !quotaStale(window, now) && window.remaining === 0))
    return 'exhausted';
  if (!windows.length || windows.some((window) => quotaStale(window, now))) return 'unknown';
  return 'available';
}
export const subscriptionStateLabel = {
  available: 'Quota available',
  exhausted: 'Quota exhausted',
  unknown: 'Awaiting usage',
  disabled: 'Disabled',
  unavailable: 'Unavailable'
};
export function subscriptionReset(file: AuthFile, now: number): number {
  if (subscriptionState(file, now) !== 'available') return Infinity;
  return Math.min(
    ...subscriptionWindows(file).flatMap((window) =>
      window.reset !== undefined && window.reset > now ? [window.reset] : []
    )
  );
}
export function compareSubscriptions(a: AuthFile, b: AuthFile, now: number): number {
  const order = { available: 0, unknown: 1, exhausted: 2, unavailable: 3, disabled: 4 };
  return (
    order[subscriptionState(a, now)] - order[subscriptionState(b, now)] ||
    subscriptionReset(a, now) - subscriptionReset(b, now) ||
    a.name.localeCompare(b.name)
  );
}
export function subscriptionSummary(files: AuthFile[], now: number) {
  const accounts = files.filter(isCodex);
  const enabled = accounts.filter((file) => !file.disabled);
  const observed = enabled.filter((file) => !file.unavailable && file.status !== 'error');
  const average = (minutes: number) => {
    const windows = observed
      .flatMap(subscriptionWindows)
      .filter((window) => window.minutes === minutes && !quotaStale(window, now));
    return {
      remaining: windows.length
        ? windows.reduce((sum, window) => sum + window.remaining, 0) / windows.length
        : undefined,
      count: windows.length
    };
  };
  const next = [...accounts].sort(
    (a, b) => subscriptionReset(a, now) - subscriptionReset(b, now)
  )[0];
  const reset = next ? subscriptionReset(next, now) : Infinity;
  return {
    enabled: enabled.length,
    total: accounts.length,
    exhausted: accounts.filter((file) => subscriptionState(file, now) === 'exhausted').length,
    hasShort: accounts.some((file) =>
      subscriptionWindows(file).some((window) => window.minutes === 300)
    ),
    short: average(300),
    weekly: average(10080),
    next: Number.isFinite(reset) ? next : undefined,
    reset
  };
}
