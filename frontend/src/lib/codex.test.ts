import { describe, expect, it } from 'vitest';
import type { AuthFile } from './api';
import {
  codexPlan,
  compareSubscriptions,
  subscriptionReset,
  subscriptionState,
  subscriptionSummary
} from './codex';
import { QUOTA_MAX_AGE } from './quota';
const now = Date.parse('2026-10-03T12:00:00Z');
function account(name: string, shortUsed = 20, weeklyUsed = 50, reset = 3600): AuthFile {
  return {
    name,
    provider: 'codex',
    quota: {
      observed_at: new Date(now).toISOString(),
      signals: {
        'x-codex-primary-used-percent': String(shortUsed),
        'x-codex-primary-window-minutes': '300',
        'x-codex-primary-reset-at': String(now / 1000 + reset),
        'x-codex-secondary-used-percent': String(weeklyUsed),
        'x-codex-secondary-window-minutes': '10080',
        'x-codex-secondary-reset-at': String(now / 1000 + 86400)
      }
    }
  };
}
describe('Codex subscriptions', () => {
  it('keeps short and weekly averages separate and excludes disabled or unavailable snapshots', () => {
    const files = [
      account('one', 20, 80),
      account('two', 40, 20),
      { ...account('disabled', 100, 100), disabled: true },
      { ...account('offline', 100, 100), unavailable: true }
    ];
    const summary = subscriptionSummary(files, now);
    expect(summary.short.remaining).toBeCloseTo(70);
    expect(summary.weekly.remaining).toBeCloseTo(50);
    expect(summary.short.count).toBe(2);
    expect(summary.enabled).toBe(3);
  });
  it('never recommends an exhausted independent window, stale data, or a disabled account', () => {
    const later = account('later', 20, 10, 7200);
    const sooner = account('sooner', 20, 10, 3600);
    const exhausted = account('exhausted', 10, 100, 60);
    const disabled = { ...account('disabled', 10, 10, 30), disabled: true };
    expect(subscriptionReset(exhausted, now)).toBe(Infinity);
    expect(subscriptionState(sooner, now + QUOTA_MAX_AGE)).toBe('unknown');
    expect(subscriptionSummary([exhausted, disabled, later, sooner], now).next?.name).toBe(
      'sooner'
    );
    expect(
      [exhausted, disabled, later, sooner]
        .sort((a, b) => compareSubscriptions(a, b, now))
        .map((file) => file.name)
    ).toEqual(['sooner', 'later', 'exhausted', 'disabled']);
  });
  it('does not mix additional model pools into subscription availability', () => {
    const file = account('main');
    file.model_quotas = {
      spark: {
        observed_at: new Date(now).toISOString(),
        signals: {
          'x-codex-additional-spark-primary-used-percent': '100',
          'x-codex-additional-spark-primary-window-minutes': '300'
        }
      }
    };
    expect(subscriptionState(file, now)).toBe('available');
    expect(subscriptionSummary([file], now).short.remaining).toBe(80);
  });
  it('reads only safe plan summaries and reported plans, not raw tokens', () => {
    expect(codexPlan({ ...account('one'), id_token: { plan_type: 'plus' } })).toBe('Plus');
    expect(codexPlan({ ...account('one'), id_token: 'raw.jwt.token' })).toBeUndefined();
  });
});
