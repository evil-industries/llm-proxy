import { expect, test, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import UsagePanel from '$lib/components/management/UsagePanel.svelte';
import ManagementApp from '$lib/components/ManagementApp.svelte';
import { MockEventSource } from './realtime-fixture';
const now = Date.now();
const account = {
  name: 'codex.json',
  provider: 'codex',
  email: 'team@example.com',
  quota: {
    observed_at: new Date(now).toISOString(),
    signals: {
      'X-Codex-Primary-Used-Percent': '25',
      'X-Codex-Primary-Window-Minutes': '300',
      'X-Codex-Primary-Reset-At': String(Math.floor(now / 1000) + 7200),
      'X-Codex-Secondary-Used-Percent': '90',
      'X-Codex-Secondary-Window-Minutes': '10080',
      'X-Codex-Secondary-Reset-At': String(Math.floor(now / 1000) + 86400)
    }
  }
};
test('shows each account and independent limits with reset timestamps and honest missing data', async () => {
  render(UsagePanel, { files: [account, { name: 'unobserved.json', provider: 'gemini' }] });
  await expect.element(page.getByText('75% left', { exact: true })).toBeVisible();
  await expect.element(page.getByText('10% left', { exact: true })).toBeVisible();
  await expect.element(page.getByText('5-hour window', { exact: true })).toBeVisible();
  await expect.element(page.getByText('7-day window', { exact: true })).toBeVisible();
  expect(document.querySelectorAll('progress')).toHaveLength(2);
  await expect
    .element(page.getByText('Remaining usage unavailable.', { exact: false }))
    .toBeVisible();
  expect(document.body.textContent).toContain('Resets ·');
});
test('marks an expired allowance as last reported without inventing a refill', async () => {
  render(UsagePanel, {
    files: [
      {
        ...account,
        quota: {
          observed_at: new Date(now - 3600000).toISOString(),
          signals: {
            'X-Codex-Primary-Used-Percent': '100',
            'X-Codex-Primary-Reset-At': String(Math.floor(now / 1000) - 1)
          }
        }
      }
    ]
  });
  await expect.element(page.getByText('0% last reported', { exact: true })).toBeVisible();
  await expect
    .element(page.getByText('Awaiting usage after reset.', { exact: false }))
    .toBeVisible();
});
test('updates usage from push events, keeps one connection across navigation, and closes on unmount', async () => {
  let used = '25';
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/auth-files'))
      return Response.json({
        files: [
          {
            ...account,
            quota: {
              ...account.quota,
              signals: { ...account.quota.signals, 'X-Codex-Primary-Used-Percent': used }
            }
          }
        ]
      });
    if (url.endsWith('/api-keys')) return Response.json({ 'api-keys': [] });
    if (url.endsWith('/config')) return Response.json({});
    return Response.json({ status: 'idle' });
  });
  vi.stubGlobal('fetch', fetcher);
  const view = await render(ManagementApp);
  await expect.element(page.getByText('75% left', { exact: true })).toBeVisible();
  const reads = fetcher.mock.calls.length;
  used = '45';
  MockEventSource.emit(1);
  await expect.element(page.getByText('55% left', { exact: true })).toBeVisible();
  expect(fetcher.mock.calls.length).toBe(reads + 1);
  await page.getByRole('navigation').getByRole('button', { name: 'Usage', exact: true }).click();
  expect(MockEventSource.instances).toHaveLength(1);
  await view.unmount();
  expect(MockEventSource.instances[0].closed).toBe(true);
});
test('fits quota cards on a narrow screen', async () => {
  await page.viewport(320, 900);
  render(UsagePanel, {
    files: [{ ...account, email: 'long-account-name-'.repeat(20) + '@example.com' }]
  });
  await expect.element(page.getByText('75% left', { exact: true })).toBeVisible();
  expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(window.innerWidth + 1);
  await page.viewport(1280, 900);
});
