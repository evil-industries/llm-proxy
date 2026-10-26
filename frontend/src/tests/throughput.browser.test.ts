import { expect, test, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import ThroughputPanel from '$lib/components/ThroughputPanel.svelte';
import { createSessionManagementClient } from '$lib/api';
import { demoThroughput } from '$lib/demo';

test('shows actual rates and timing and keeps demo data offline', async () => {
  const fetcher = vi.fn();
  render(ThroughputPanel, {
    client: createSessionManagementClient(fetcher),
    initialData: demoThroughput
  });
  await expect.element(page.getByText('84.8', { exact: false })).toBeVisible();
  await expect.element(page.getByText('842 ms', { exact: true })).toBeVisible();
  await expect.element(page.getByText('25,440 output tokens', { exact: false })).toBeVisible();
  expect(fetcher).not.toHaveBeenCalled();
});

test('refreshes metrics, marks errors unavailable, recovers and aborts on unmount', async () => {
  let fail = false;
  let signal: AbortSignal | undefined;
  const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    signal = init?.signal ?? undefined;
    return fail ? new Response(null, { status: 503 }) : Response.json(demoThroughput);
  });
  const view = await render(ThroughputPanel, { client: createSessionManagementClient(fetcher) });
  await expect.element(page.getByText('842 ms', { exact: true })).toBeVisible();
  fail = true;
  document.dispatchEvent(new Event('visibilitychange'));
  await expect
    .element(page.getByRole('status'))
    .toHaveTextContent('Throughput unavailable. Retrying automatically…');
  expect(document.querySelector('.metrics')?.textContent).not.toContain('84.8');
  fail = false;
  document.dispatchEvent(new Event('visibilitychange'));
  await expect.element(page.getByText('842 ms', { exact: true })).toBeVisible();
  await view.unmount();
  expect(signal?.aborted).toBe(true);
  const calls = fetcher.mock.calls.length;
  document.dispatchEvent(new Event('visibilitychange'));
  expect(fetcher.mock.calls).toHaveLength(calls);
});

test('shows disabled collection and missing timing honestly', async () => {
  const client = createSessionManagementClient(vi.fn());
  const view = await render(ThroughputPanel, {
    client,
    initialData: { ...demoThroughput, enabled: false }
  });
  await expect.element(page.getByText('Enable usage statistics', { exact: false })).toBeVisible();
  expect(document.querySelector('.metrics')?.textContent).not.toContain('84.8');
  await view.unmount();
  render(ThroughputPanel, {
    client,
    initialData: {
      ...demoThroughput,
      metrics: {
        ...demoThroughput.metrics,
        generation_tokens_per_second: null,
        average_ttft_ms: null
      }
    }
  });
  expect(document.querySelectorAll('.metric strong')[2].textContent).toContain('—');
  expect(document.querySelectorAll('.metric strong')[3].textContent).toBe('—');
});
