import { expect, test, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import ManagementApp from '$lib/components/ManagementApp.svelte';
import type { AuthFile } from '$lib/api';

test('runtime credentials can be disabled and re-enabled without offering file deletion', async () => {
  const runtime: AuthFile = {
    name: 'aistudio-connected',
    auth_index: 'runtime-index',
    provider: 'aistudio',
    runtime_only: true,
    disabled: false
  };
  const saved: AuthFile = { name: 'saved.json', auth_index: 'saved-index', provider: 'codex' };
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const path = new URL(String(input), window.location.origin).pathname;
    if (path.endsWith('/auth-files/status')) {
      const body = JSON.parse(String(init?.body));
      expect(body.name).toBe(runtime.name);
      expect(body.auth_index).toBe(runtime.auth_index);
      runtime.disabled = body.disabled;
      return Response.json({ status: 'ok' });
    }
    if (path.endsWith('/auth-files')) return Response.json({ files: [runtime, saved] });
    if (path.endsWith('/api-keys')) return Response.json({ 'api-keys': ['test-key'] });
    if (path.endsWith('/config')) return Response.json({});
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal('fetch', fetch);
  render(ManagementApp);
  await expect.element(page.getByRole('button', { name: 'Refresh data' })).toBeEnabled();
  await page
    .getByRole('navigation')
    .getByRole('button', { name: 'Credentials', exact: true })
    .click();
  await expect.element(page.getByText('Runtime credential · No saved file')).toBeVisible();
  await expect
    .element(page.getByRole('button', { name: `Delete ${runtime.name}` }))
    .not.toBeInTheDocument();
  await expect.element(page.getByRole('button', { name: `Delete ${saved.name}` })).toBeVisible();
  const toggle = page.getByRole('switch', { name: `Enable ${runtime.name}` });
  await expect.element(toggle).toBeChecked();
  await toggle.click();
  await expect.element(page.getByText('Credential disabled.', { exact: true })).toBeVisible();
  await expect.element(toggle).not.toBeChecked();
  await expect.element(toggle).toBeEnabled();
  await toggle.click();
  await expect.element(page.getByText('Credential enabled.', { exact: true })).toBeVisible();
  await expect.element(toggle).toBeChecked();
  expect(
    fetch.mock.calls.filter(([input]) => String(input).endsWith('/auth-files/status'))
  ).toHaveLength(2);
});
