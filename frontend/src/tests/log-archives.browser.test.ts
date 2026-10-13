import { MockEventSource } from './realtime-fixture';
import { expect, test, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import LogsPanel from '$lib/components/LogsPanel.svelte';
import { createSessionManagementClient } from '$lib/api';

const archived = {
  source: 's3',
  key: 'logs/request abc.log.gz',
  name: 'request abc.log.gz',
  size: 1000,
  modified: 1789635900,
  kind: 'request'
};
const local = {
  source: 'local',
  key: 'main.log',
  name: 'main.log',
  size: 2000,
  modified: 1789635900,
  kind: 'application'
};

test('lists local and S3 log downloads, paginates empty transitions, and preserves loaded files on failure', async () => {
  const requests: URL[] = [];
  const archiveResponse = vi
    .fn()
    .mockResolvedValueOnce(Response.json({ files: [local], next_cursor: 'local-end' }))
    .mockResolvedValueOnce(Response.json({ files: [], next_cursor: 'remote-start' }))
    .mockResolvedValueOnce(Response.json({ files: [archived], next_cursor: 'remote-page2' }))
    .mockResolvedValueOnce(Response.json({}, { status: 502 }))
    .mockResolvedValueOnce(Response.json({ files: [archived], next_cursor: '' }));
  const client = createSessionManagementClient(async (input) => {
    const url = new URL(String(input), location.origin);
    if (url.pathname.endsWith('/log-archives')) {
      requests.push(url);
      return archiveResponse();
    }
    return Response.json({ lines: [], 'line-count': 0, 'latest-timestamp': 0, 'next-cursor': '' });
  });
  render(LogsPanel, { client });
  await page.getByRole('tab', { name: 'Downloads' }).click();
  const localDownload = page.getByRole('link', { name: 'Download main.log from local storage' });
  await expect
    .element(localDownload)
    .toHaveAttribute('href', '/api/management/log-archives/download?source=local&key=main.log');
  await expect.element(localDownload).toHaveAttribute('download', 'main.log');
  await page.getByRole('button', { name: 'Load more archives' }).click();
  await expect.poll(() => requests.at(-1)?.searchParams.get('cursor')).toBe('local-end');
  await page.getByRole('button', { name: 'Load more archives' }).click();
  const s3Download = page.getByRole('link', { name: 'Download request abc.log.gz from S3' });
  await expect
    .element(s3Download)
    .toHaveAttribute(
      'href',
      '/api/management/log-archives/download?source=s3&key=logs%2Frequest+abc.log.gz'
    );
  await expect.element(s3Download).toHaveAttribute('download', archived.name);
  expect(requests.at(-1)?.searchParams.get('cursor')).toBe('remote-start');
  await page.getByRole('button', { name: 'Load more archives' }).click();
  await expect.element(page.getByRole('alert')).toBeVisible();
  await expect.element(localDownload).toBeVisible();
  await expect.element(s3Download).toBeVisible();
  await page.getByRole('button', { name: 'Load more archives' }).click();
  await expect.element(page.getByText('2 files loaded')).toBeVisible();
  await expect
    .element(page.getByRole('button', { name: 'Load more archives' }))
    .not.toBeInTheDocument();
  expect(requests.map((url) => url.searchParams.get('cursor'))).toEqual([
    null,
    'local-end',
    'remote-start',
    'remote-page2',
    'remote-page2'
  ]);
  MockEventSource.emit(4);
});

test('allows loading S3 archives when the initial local page is empty', async () => {
  const client = createSessionManagementClient(async (input) => {
    const url = new URL(String(input), location.origin);
    if (!url.pathname.endsWith('/log-archives'))
      return Response.json({
        lines: [],
        'line-count': 0,
        'latest-timestamp': 0,
        'next-cursor': ''
      });
    return Response.json(
      url.searchParams.has('cursor')
        ? { files: [archived], next_cursor: '' }
        : { files: [], next_cursor: 's3' }
    );
  });
  render(LogsPanel, { client });
  await page.getByRole('tab', { name: 'Downloads' }).click();
  await page.getByRole('button', { name: 'Load more archives' }).click();
  await expect
    .element(page.getByRole('link', { name: 'Download request abc.log.gz from S3' }))
    .toBeVisible();
});
