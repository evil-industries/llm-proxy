import { describe, expect, it } from 'vitest';
import { accountQuota, quotaStale, quotaExpired, QUOTA_MAX_AGE } from './quota';
const observed = Date.parse('2026-09-20T12:00:00Z');
const quota = (signals: Record<string, string>, at = observed) => ({
  observed_at: new Date(at).toISOString(),
  signals
});

describe('provider allowances', () => {
  it('reads Codex windows case-insensitively and anchors relative resets to the observation', () => {
    const result = accountQuota({
      name: 'account',
      provider: 'codex',
      quota: quota({
        'X-Codex-Primary-Used-Percent': '27.5',
        'X-Codex-Primary-Window-Minutes': '300',
        'X-Codex-Primary-Reset-After-Seconds': '3600',
        'X-Codex-Secondary-Used-Percent': '100',
        'X-Codex-Secondary-Window-Minutes': '10080',
        'X-Codex-Secondary-Reset-At': String(observed / 1000 + 7200),
        'X-Codex-Credits-Balance': '12.5',
        'X-Codex-Rate-Limit-Reset-Credits-Available-Count': '0'
      })
    });
    expect(result.windows).toMatchObject([
      { label: '5-hour window', remaining: 72.5, reset: observed + 3600000 },
      { label: '7-day window', remaining: 0, reset: observed + 7200000 }
    ]);
    expect(result.resetCredits).toBe(0);
    expect(result.credits).toContain('12.5');
  });
  it('uses newest observations across models and deduplicates named pools while keeping separate pools', () => {
    const result = accountQuota({
      name: 'account',
      provider: 'codex',
      quota: quota({
        'x-codex-active-limit': 'codex_spark',
        'x-codex-spark-limit-name': 'Spark',
        'x-codex-primary-used-percent': '80'
      }),
      model_quotas: {
        model: quota(
          {
            'x-codex-additional-spark-primary-used-percent': '20',
            'x-codex-additional-spark-limit-name': 'Spark',
            'x-codex-primary-used-percent': '10'
          },
          observed + 1000
        )
      }
    });
    expect(result.windows).toHaveLength(2);
    expect(result.windows.find((w) => w.key === 'spark:primary')?.remaining).toBe(80);
    expect(result.windows.find((w) => w.key === 'primary')?.remaining).toBe(90);
  });
  it('converts Claude utilization fractions and keeps model-specific windows', () => {
    const result = accountQuota({
      name: 'account',
      type: 'claude',
      quota: quota({
        'anthropic-ratelimit-unified-5h-utilization': '0.25',
        'anthropic-ratelimit-unified-5h-reset': String(observed / 1000 + 60),
        'anthropic-ratelimit-unified-7d-sonnet-utilization': '0.5'
      })
    });
    expect(result.windows).toMatchObject([
      { label: '5-hour window', remaining: 75 },
      { label: '7d sonnet', remaining: 50 }
    ]);
  });
  it('does not interpret cooldowns, missing observations or malformed values as allowances', () => {
    for (const file of [
      {
        name: 'unknown',
        provider: 'gemini',
        quota: quota({ 'x-codex-primary-used-percent': '2' })
      },
      {
        name: 'missing',
        provider: 'codex',
        quota: { signals: { 'x-codex-primary-used-percent': '2' } }
      },
      {
        name: 'cooldown',
        provider: 'codex',
        quota: { next_recover_at: '2026-09-21T12:00:00Z', exceeded: true }
      },
      ...['', 'NaN', 'Infinity', '-1', '101'].map((used) => ({
        name: 'invalid',
        provider: 'codex',
        quota: quota({ 'x-codex-primary-used-percent': used })
      }))
    ])
      expect(accountQuota(file).windows).toEqual([]);
  });
  it('retains last reported values after reset and marks old observations stale', () => {
    const window = {
      key: 'primary',
      label: 'Primary',
      remaining: 0,
      observed,
      reset: observed + 1000
    };
    expect(quotaExpired(window, observed)).toBe(false);
    expect(quotaExpired(window, observed + 1000)).toBe(true);
    expect(quotaStale(window, observed + 1000)).toBe(true);
    expect(window.remaining).toBe(0);
    expect(quotaStale({ ...window, reset: undefined }, observed + QUOTA_MAX_AGE)).toBe(true);
  });
});
