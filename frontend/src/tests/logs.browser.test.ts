import { MockEventSource } from './realtime-fixture';
import { expect, test, vi } from 'vitest';
import { page, userEvent } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import LogsPanel from '$lib/components/LogsPanel.svelte';
import { createSessionManagementClient } from '$lib/api';

const file = { name: 'error-2026-req-abc.log', size: 100000, modified: 1789635900, kind: 'error' };
function logBatch(start: number, count: number, cursor: string) {
  return Response.json({
    lines: Array.from({ length: count }, (_, i) => `[info] backlog ${start + i}`),
    'line-count': count,
    'latest-timestamp': 1789635901,
    'next-cursor': cursor
  });
}
function fixture(lines = ['[info] first request', '[error] failed request']) {
  const requests: URL[] = [];
  const get = vi.fn(async (url: URL): Promise<Response> => {
    if (url.pathname.endsWith('/request-logs')) return Response.json({ files: [file] });
    if (url.pathname.includes('/request-logs/')) {
      const offset = Number(url.searchParams.get('offset'));
      return Response.json({
        name: file.name,
        text: offset ? '\nremaining detail' : 'first detail',
        next_offset: offset ? 100000 : 65536,
        size: 100000,
        modified: file.modified,
        has_more: !offset
      });
    }
    return Response.json({
      lines: url.searchParams.has('cursor') ? [] : lines,
      'line-count': lines.length,
      'latest-timestamp': 1789635900,
      'next-cursor': 'cursor-1'
    });
  });
  const client = createSessionManagementClient(async (input) => {
    const url = new URL(String(input), location.origin);
    requests.push(url);
    return get(url);
  });
  return { client, requests, get, poll: () => MockEventSource.emit(4) };
}

test('filters loaded history and opens full log details with keyboard and restores focus', async () => {
  const { client } = fixture();
  render(LogsPanel, { client });
  await expect.element(page.getByRole('button', { name: 'Inspect log line 1' })).toBeVisible();
  await page.getByLabelText('Search logs').fill('FAILED');
  await expect
    .element(page.getByRole('button', { name: 'Inspect log line 1' }))
    .not.toBeInTheDocument();
  await page.getByLabelText('Log severity', { exact: true }).click();
  await page.getByRole('option', { name: 'info', exact: true }).click();
  await expect.element(page.getByText('No matching log lines')).toBeVisible();
  await page.getByRole('button', { name: 'Clear filters' }).click();
  const row = page.getByRole('button', { name: 'Inspect log line 2' });
  await row.click();
  await expect.element(page.getByRole('dialog')).toBeVisible();
  await expect
    .element(page.getByRole('region', { name: 'Full log message' }))
    .toHaveTextContent('[error] failed request');
  await page.getByRole('button', { name: 'Close', exact: true }).click();
  await expect.element(row).toHaveFocus();
  const element = (await row.element()) as HTMLButtonElement;
  element.focus();
  await userEvent.keyboard('{Enter}');
  await expect.element(page.getByRole('dialog')).toBeVisible();
  await userEvent.keyboard('{Escape}');
  await expect.element(row).toHaveFocus();
});

test('updates on push events from the returned cursor, pauses, retains history after errors and marks cursor resets', async () => {
  const state = fixture();
  render(LogsPanel, { client: state.client });
  await expect.element(page.getByRole('button', { name: 'Inspect log line 1' })).toBeVisible();
  await page.getByRole('button', { name: 'Pause live' }).click();
  state.poll();
  expect(state.requests).toHaveLength(1);
  state.get.mockResolvedValueOnce(
    Response.json({
      lines: ['[info] appended'],
      'line-count': 1,
      'latest-timestamp': 1789635901,
      'next-cursor': 'cursor-2'
    })
  );
  await page.getByRole('button', { name: 'Resume live' }).click();
  await expect.element(page.getByText('[info] appended', { exact: true })).toBeVisible();
  expect(state.requests[1].searchParams.get('cursor')).toBe('cursor-1');
  state.get.mockResolvedValueOnce(Response.json({}, { status: 502 }));
  state.poll();
  await expect.element(page.getByRole('alert')).toHaveTextContent('Loaded lines are retained');
  await expect.element(page.getByText('[info] appended', { exact: true })).toBeVisible();
  await expect.element(page.getByRole('button', { name: 'Resume live' })).toBeVisible();
  expect(state.requests[2].searchParams.get('cursor')).toBe('cursor-2');
  state.get.mockResolvedValueOnce(
    Response.json({
      lines: ['[warn] after rotation'],
      'line-count': 1,
      'latest-timestamp': 1789635902,
      'next-cursor': 'cursor-3',
      'cursor-reset': true
    })
  );
  await page.getByRole('button', { name: 'Refresh logs' }).click();
  await expect.element(page.getByText(/Log cursor reset: the server rotated/)).toBeVisible();
  await expect.element(page.getByText('[info] first request', { exact: true })).toBeVisible();
  state.get.mockResolvedValueOnce(
    Response.json({
      lines: ['[info] newest'],
      'line-count': 1,
      'latest-timestamp': 1789635903,
      'next-cursor': 'cursor-4'
    })
  );
  await page.getByRole('button', { name: 'Load latest (replace history)' }).click();
  expect(state.requests.at(-1)?.searchParams.has('cursor')).toBe(false);
  await expect.element(page.getByText('[info] newest', { exact: true })).toBeVisible();
  await expect
    .element(page.getByText('[info] first request', { exact: true }))
    .not.toBeInTheDocument();
});

test('prevents overlapping cursor reads and aborts an in-flight read on unmount', async () => {
  const state = fixture();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  state.get.mockImplementationOnce(async () => {
    await gate;
    return Response.json({
      lines: ['[info] first'],
      'line-count': 1,
      'latest-timestamp': 1,
      'next-cursor': 'one'
    });
  });
  const result = await render(LogsPanel, { client: state.client });
  await expect.poll(() => state.requests.length).toBe(1);
  state.poll();
  state.poll();
  expect(state.requests).toHaveLength(1);
  await result.unmount();
  release();
  expect(document.querySelector('[aria-label="Log inspection"]')).toBeNull();
});

test('keeps more than one rendered page without truncating loaded history and exports only visible lines', async () => {
  const state = fixture(Array.from({ length: 200 }, (_, i) => `[info] original ${i}`));
  const urls = vi.spyOn(URL, 'createObjectURL');
  render(LogsPanel, { client: state.client });
  await expect.element(page.getByText('[info] original 0', { exact: true })).toBeVisible();
  state.get.mockResolvedValueOnce(
    Response.json({
      lines: ['[error] later'],
      'line-count': 1,
      'latest-timestamp': 2,
      'next-cursor': 'next'
    })
  );
  await page.getByRole('button', { name: 'Refresh logs' }).click();
  await expect.element(page.getByText('[error] later', { exact: true })).toBeVisible();
  await expect.element(page.getByText('Page 2 of 2')).toBeVisible();
  await page.getByRole('button', { name: 'Download visible' }).click();
  const blob = urls.mock.calls.at(-1)?.[0] as Blob;
  expect(await blob.text()).toBe('[error] later\n');
  await page.getByRole('button', { name: 'Previous lines' }).click();
  await expect.element(page.getByText('[info] original 0', { exact: true })).toBeVisible();
  expect(document.querySelectorAll('.log-row')).toHaveLength(200);
});

test('lists saved request metadata, looks up exact IDs and appends byte-offset preview pages', async () => {
  const state = fixture();
  render(LogsPanel, { client: state.client });
  await page.getByRole('tab', { name: 'Request logs', exact: true }).click();
  await expect.element(page.getByText(file.name, { exact: true })).toBeVisible();
  await page.getByLabelText('Request ID', { exact: true }).fill('missing');
  await page.getByRole('button', { name: 'Find request' }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent('No saved log matches');
  await page.getByLabelText('Request ID', { exact: true }).fill('req-abc');
  await page.getByRole('button', { name: 'Find request' }).click();
  await expect
    .element(page.getByRole('region', { name: 'Full log message' }))
    .toHaveTextContent('first detail');
  await page.getByRole('button', { name: 'Load more', exact: true }).click();
  await expect
    .element(page.getByRole('region', { name: 'Full log message' }))
    .toHaveTextContent('first detail remaining detail');
  expect(state.requests.at(-1)?.searchParams.get('offset')).toBe('65536');
  await expect
    .element(page.getByRole('button', { name: 'Load more', exact: true }))
    .not.toBeInTheDocument();
  const link = page.getByRole('link', { name: 'Download complete file' });
  await expect
    .element(link)
    .toHaveAttribute('href', '/api/management/request-logs/error-2026-req-abc.log/download');
});

test('refuses to mix a changed request file into an existing preview', async () => {
  const state = fixture();
  render(LogsPanel, { client: state.client });
  await page.getByRole('tab', { name: 'Request logs', exact: true }).click();
  await page.getByRole('button', { name: `Preview ${file.name}` }).click();
  await expect.element(page.getByText('first detail', { exact: true })).toBeVisible();
  state.get.mockResolvedValueOnce(
    Response.json({
      name: file.name,
      text: 'different contents',
      next_offset: 100000,
      size: 100000,
      modified: file.modified + 1,
      has_more: false
    })
  );
  await page.getByRole('button', { name: 'Load more', exact: true }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent('file changed');
  await expect
    .element(page.getByText('different contents', { exact: true }))
    .not.toBeInTheDocument();
});

test.each([320, 1280])('contains long lines and request previews at %i pixels', async (width) => {
  await page.viewport(width, 900);
  const state = fixture(['[error] ' + 'long-diagnostic-'.repeat(300)]);
  render(LogsPanel, { client: state.client });
  await expect.element(page.getByRole('region', { name: 'Log output' })).toBeVisible();
  expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(width + 1);
  await page.getByRole('switch', { name: 'Wrap log lines' }).click();
  const output = await page.getByRole('region', { name: 'Log output' }).element();
  expect(output.scrollWidth).toBeGreaterThan(output.clientWidth);
  expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(width + 1);
  await page.getByRole('button', { name: 'Inspect log line 1' }).click();
  await expect.element(page.getByRole('dialog')).toBeVisible();
  const dialog = await page.getByRole('dialog').element();
  const rect = dialog.getBoundingClientRect();
  expect(rect.left).toBeGreaterThanOrEqual(0);
  expect(rect.right).toBeLessThanOrEqual(width + 1);
  expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth + 1);
  await userEvent.keyboard('{Escape}');
});

test('offers enabling disabled file logging without polling the unavailable endpoint', async () => {
  const state = fixture();
  const refresh = vi.fn(async () => {});
  const set = vi.spyOn(state.client, 'setBoolean').mockResolvedValue();
  render(LogsPanel, {
    client: state.client,
    config: { 'logging-to-file': false },
    onrefresh: refresh
  });
  await expect
    .element(page.getByRole('heading', { name: 'File logging is disabled' }))
    .toBeVisible();
  state.poll();
  expect(state.requests).toHaveLength(0);
  await page.getByRole('button', { name: 'Enable file logging' }).click();
  expect(set).toHaveBeenCalledWith('logging-to-file', true, expect.any(AbortSignal));
  expect(refresh).toHaveBeenCalledOnce();
});

test.each([320, 1280])(
  'keeps request file actions and preview readable at %i pixels with doubled text',
  async (width) => {
    await page.viewport(width, 900);
    const state = fixture();
    const longFile = { ...file, name: `request-${'long-name-'.repeat(18)}req-abc.log` };
    state.get.mockImplementation(async (url) =>
      url.pathname.endsWith('/request-logs')
        ? Response.json({ files: [longFile] })
        : url.pathname.includes('/request-logs/')
          ? Response.json({
              name: longFile.name,
              text: 'request body ' + 'content-'.repeat(3000),
              next_offset: 65536,
              size: 100000,
              modified: file.modified,
              has_more: true
            })
          : Response.json({
              lines: [],
              'line-count': 0,
              'latest-timestamp': 1,
              'next-cursor': 'one'
            })
    );
    render(LogsPanel, { client: state.client });
    await page.getByRole('tab', { name: 'Request logs', exact: true }).click();
    await expect
      .element(page.getByRole('button', { name: `Preview ${longFile.name}` }))
      .toBeVisible();
    const fonts = [
      ...document.querySelectorAll<HTMLElement>('[aria-label="Log inspection"] *')
    ].map((element) => [element, parseFloat(getComputedStyle(element).fontSize)] as const);
    for (const [element, size] of fonts) element.style.fontSize = `${size * 2}px`;
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(width + 1);
    const previewButton = page.getByRole('button', { name: `Preview ${longFile.name}` });
    const buttonRect = (await previewButton.element()).getBoundingClientRect();
    expect(buttonRect.right).toBeLessThanOrEqual(width + 1);
    await previewButton.click();
    await expect
      .element(page.getByRole('region', { name: 'Full log message' }))
      .toHaveTextContent('request body');
    const dialog = await page.getByRole('dialog').element();
    const dialogFonts = [...dialog.querySelectorAll<HTMLElement>('*')].map(
      (element) => [element, parseFloat(getComputedStyle(element).fontSize)] as const
    );
    for (const [element, size] of dialogFonts) element.style.fontSize = `${size * 2}px`;
    const rect = dialog.getBoundingClientRect();
    expect(rect.left).toBeGreaterThanOrEqual(0);
    expect(rect.right).toBeLessThanOrEqual(width + 1);
    expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth + 1);
    await page.getByRole('button', { name: 'Load more', exact: true }).click();
    await page.getByRole('button', { name: 'Close', exact: true }).click();
    await expect.element(previewButton).toHaveFocus();
  }
);

test('keeps an existing request preview on failure and retries the same byte offset', async () => {
  const state = fixture();
  render(LogsPanel, { client: state.client });
  await page.getByRole('tab', { name: 'Request logs', exact: true }).click();
  await page.getByRole('button', { name: `Preview ${file.name}` }).click();
  await expect.element(page.getByText('first detail', { exact: true })).toBeVisible();
  state.get.mockResolvedValueOnce(
    Response.json({ error: 'secret-upstream-body' }, { status: 502 })
  );
  await page.getByRole('button', { name: 'Load more', exact: true }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent('The server could not complete');
  expect(document.body.textContent).not.toContain('secret-upstream-body');
  await expect.element(page.getByText('first detail', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Load more', exact: true }).click();
  expect(state.requests.at(-1)?.searchParams.get('offset')).toBe('65536');
  await expect
    .element(page.getByRole('region', { name: 'Full log message' }))
    .toHaveTextContent('remaining detail');
});

test('copies the current page and announces detail copy results inside the dialog', async () => {
  const state = fixture();
  const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
  render(LogsPanel, { client: state.client });
  await expect.element(page.getByRole('button', { name: 'Inspect log line 1' })).toBeVisible();
  await page.getByRole('button', { name: 'Copy visible', exact: true }).click();
  expect(copy).toHaveBeenLastCalledWith('[info] first request\n[error] failed request');
  await page.getByRole('button', { name: 'Inspect log line 2' }).click();
  await page.getByRole('button', { name: 'Copy message', exact: true }).click();
  expect(copy).toHaveBeenLastCalledWith('[error] failed request');
  await expect
    .element(page.getByRole('dialog').getByRole('status'))
    .toHaveTextContent('Copied to clipboard.');
});

test('drains a large backlog serially without another push event and keeps rendering paginated', async () => {
  const state = fixture();
  render(LogsPanel, { client: state.client });
  await expect.element(page.getByText('[info] first request', { exact: true })).toBeVisible();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  state.get
    .mockResolvedValueOnce(logBatch(0, 1000, 'batch-1'))
    .mockImplementationOnce(async () => {
      await gate;
      return logBatch(1000, 1000, 'batch-2');
    })
    .mockResolvedValueOnce(logBatch(2000, 501, 'batch-3'));
  state.poll();
  await expect.poll(() => state.requests.length).toBe(3);
  await expect.element(page.getByText('Catching up…', { exact: true })).toBeVisible();
  state.poll();
  state.poll();
  expect(state.requests).toHaveLength(3);
  release();
  await expect.element(page.getByText('[info] backlog 2500', { exact: true })).toBeVisible();
  await expect.element(page.getByText('Live updates', { exact: true })).toBeVisible();
  expect(state.requests).toHaveLength(4);
  expect(state.requests.slice(1).map((url) => url.searchParams.get('cursor'))).toEqual([
    'cursor-1',
    'batch-1',
    'batch-2'
  ]);
  expect(document.querySelectorAll('.log-row').length).toBeLessThanOrEqual(200);
  await expect.element(page.getByText('Page 13 of 13')).toBeVisible();
});

test('allows pausing while catching up and resumes from the last completed cursor', async () => {
  const state = fixture();
  render(LogsPanel, { client: state.client });
  await expect.element(page.getByText('[info] first request', { exact: true })).toBeVisible();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  state.get
    .mockResolvedValueOnce(logBatch(0, 1000, 'batch-1'))
    .mockImplementationOnce(async () => {
      await gate;
      return logBatch(1000, 1000, 'batch-2');
    })
    .mockResolvedValueOnce(logBatch(2000, 1, 'batch-3'));
  state.poll();
  await expect.poll(() => state.requests.length).toBe(3);
  await page.getByRole('button', { name: 'Pause live', exact: true }).click();
  release();
  await expect
    .element(page.getByRole('button', { name: 'Refresh logs', exact: true }))
    .toBeEnabled();
  state.poll();
  expect(state.requests).toHaveLength(3);
  await expect.element(page.getByText('Paused', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Resume live', exact: true }).click();
  await expect.element(page.getByText('[info] backlog 2000', { exact: true })).toBeVisible();
  expect(state.requests[3].searchParams.get('cursor')).toBe('batch-2');
});

test('retains drained batches after failure and retries from the last successful cursor', async () => {
  const state = fixture();
  render(LogsPanel, { client: state.client });
  await expect.element(page.getByText('[info] first request', { exact: true })).toBeVisible();
  state.get
    .mockResolvedValueOnce(logBatch(0, 1000, 'batch-1'))
    .mockResolvedValueOnce(Response.json({}, { status: 502 }));
  state.poll();
  await expect.element(page.getByRole('alert')).toHaveTextContent('Loaded lines are retained');
  await expect.element(page.getByText('[info] backlog 999', { exact: true })).toBeVisible();
  state.get.mockResolvedValueOnce(logBatch(1000, 1, 'batch-2'));
  await page.getByRole('button', { name: 'Resume live', exact: true }).click();
  await expect.element(page.getByText('[info] backlog 1000', { exact: true })).toBeVisible();
  expect(state.requests.at(-1)?.searchParams.get('cursor')).toBe('batch-1');
});

test('pauses instead of spinning when a full batch does not advance its cursor', async () => {
  const state = fixture();
  render(LogsPanel, { client: state.client });
  await expect.element(page.getByText('[info] first request', { exact: true })).toBeVisible();
  state.get.mockResolvedValueOnce(logBatch(0, 1000, 'cursor-1'));
  state.poll();
  await expect.element(page.getByRole('alert')).toHaveTextContent('Log cursor did not advance');
  state.poll();
  expect(state.requests).toHaveLength(2);
  await expect.element(page.getByText('[info] first request', { exact: true })).toBeVisible();
});
