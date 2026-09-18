import { expect, test, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import NotificationsPanel from '$lib/components/management/NotificationsPanel.svelte';
import {
  createSessionManagementClient,
  type NotificationSettings,
  type NotificationUpdate
} from '$lib/api';

function fixture(initial: Partial<NotificationSettings> = {}) {
  let saved: NotificationSettings = {
    enabled: true,
    reset_notifications: true,
    banked_reset_notifications: true,
    url: 'https://ntfy.example.com',
    topic: 'quota-alerts',
    warning_percent: 20,
    critical_percent: 5,
    token_configured: true,
    status: { enabled: true, configured: true, in_flight: false },
    ...initial
  };
  const writes: NotificationUpdate[] = [];
  const state = { fail: false, tests: 0 };
  const fetcher = vi.fn<typeof fetch>(async (input, init) => {
    if (state.fail) return Response.json({ error: 'secret-token-do-not-display' }, { status: 502 });
    if (String(input).endsWith('/test')) {
      state.tests++;
      expect(init?.body).toBe('{}');
      return Response.json({ status: 'ok' });
    }
    if (init?.method === 'PUT') {
      const update = JSON.parse(String(init.body)) as NotificationUpdate;
      writes.push(update);
      saved = {
        ...saved,
        enabled: update.enabled,
        reset_notifications: update.reset_notifications,
        banked_reset_notifications: update.banked_reset_notifications,
        url: update.url,
        topic: update.topic,
        warning_percent: update.warning_percent,
        critical_percent: update.critical_percent,
        token_configured: update.clear_token ? false : !!update.token || saved.token_configured,
        status: {
          enabled: update.enabled,
          configured: !!update.url && !!update.topic,
          in_flight: false
        }
      };
    }
    return Response.json(saved);
  });
  const client = createSessionManagementClient(fetcher);
  return { client, writes, state, fetcher };
}

test('saves quota defaults, preserves or explicitly replaces and clears the write-only token', async () => {
  const { client, writes, state } = fixture();
  render(NotificationsPanel, { client });
  await expect.element(page.getByLabelText('Warning remaining quota (%)')).toHaveValue(20);
  await expect.element(page.getByLabelText('Critical remaining quota (%)')).toHaveValue(5);
  const token = page.getByLabelText('Bearer token (optional)');
  await expect.element(token).toHaveValue('');
  await page.getByLabelText('Topic', { exact: true }).fill('new-topic');
  await expect.element(page.getByRole('button', { name: 'Send test notification' })).toBeDisabled();
  await page.getByRole('button', { name: 'Save notifications' }).click();
  await expect
    .element(page.getByText('Notification settings saved.', { exact: true }))
    .toBeVisible();
  expect(writes[0]).not.toHaveProperty('token');
  expect(writes[0]).not.toHaveProperty('clear_token');
  await token.fill('private-replacement');
  await page.getByRole('button', { name: 'Save notifications' }).click();
  await expect.element(token).toHaveValue('');
  expect(writes[1].token).toBe('private-replacement');
  await page.getByLabelText('Remove saved token').click();
  await expect.element(token).toBeDisabled();
  await page.getByRole('button', { name: 'Save notifications' }).click();
  await expect.element(page.getByText('No token is configured.')).toBeVisible();
  expect(writes[2].clear_token).toBe(true);
  expect(writes[2]).not.toHaveProperty('token');
  expect(state.tests).toBe(0);
  await page.getByRole('button', { name: 'Send test notification' }).click();
  await expect.element(page.getByText('Test notification sent.', { exact: true })).toBeVisible();
  expect(state.tests).toBe(1);
});

test('rejects inverted thresholds and preserves drafts on safe save failure', async () => {
  const { client, writes, state } = fixture();
  render(NotificationsPanel, { client });
  await expect.element(page.getByLabelText('Critical remaining quota (%)')).toHaveValue(5);
  await page.getByLabelText('Critical remaining quota (%)').fill('25');
  await page.getByRole('button', { name: 'Save notifications' }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent('Critical quota must be lower');
  expect(writes).toHaveLength(0);
  await page.getByLabelText('Critical remaining quota (%)').fill('10');
  state.fail = true;
  await page.getByRole('button', { name: 'Save notifications' }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent('The server could not complete');
  await expect.element(page.getByLabelText('Critical remaining quota (%)')).toHaveValue(10);
  expect(document.body.textContent).not.toContain('secret-token-do-not-display');
});

test('does not send tests for an unconfigured destination', async () => {
  const { client, state } = fixture({
    enabled: false,
    url: '',
    topic: '',
    token_configured: false,
    status: { enabled: false, configured: false, in_flight: false }
  });
  render(NotificationsPanel, { client });
  await expect.element(page.getByLabelText('ntfy server URL')).toHaveValue('');
  await expect.element(page.getByRole('switch', { name: 'Enable quota alerts' })).not.toBeChecked();
  await expect
    .element(page.getByRole('switch', { name: 'Quota reset notifications', exact: true }))
    .toBeChecked();
  await expect
    .element(page.getByRole('switch', { name: 'Codex banked reset notifications' }))
    .toBeChecked();
  await expect.element(page.getByRole('button', { name: 'Send test notification' })).toBeDisabled();
  expect(state.tests).toBe(0);
});

test('saves reset notification choices independently without enabling automatic delivery', async () => {
  const { client, writes } = fixture({
    enabled: false,
    status: { enabled: false, configured: true, in_flight: false }
  });
  render(NotificationsPanel, { client });
  const reset = page.getByRole('switch', { name: 'Quota reset notifications', exact: true });
  const banked = page.getByRole('switch', { name: 'Codex banked reset notifications' });
  await expect.element(reset).toBeChecked();
  await expect.element(banked).toBeChecked();
  await reset.click();
  await expect.element(page.getByRole('button', { name: 'Send test notification' })).toBeDisabled();
  await page.getByRole('button', { name: 'Save notifications' }).click();
  await expect.element(page.getByRole('button', { name: 'Save notifications' })).toBeDisabled();
  expect(writes[0]).toMatchObject({
    enabled: false,
    reset_notifications: false,
    banked_reset_notifications: true
  });
  await expect.element(reset).not.toBeChecked();
  await expect.element(banked).toBeChecked();
  await reset.click();
  await banked.click();
  await page.getByRole('button', { name: 'Save notifications' }).click();
  await expect.element(page.getByRole('button', { name: 'Save notifications' })).toBeDisabled();
  expect(writes[1]).toMatchObject({
    enabled: false,
    reset_notifications: true,
    banked_reset_notifications: false
  });
  await page.getByRole('button', { name: 'Refresh notification status' }).click();
  await expect
    .element(page.getByRole('button', { name: 'Refresh notification status' }))
    .toBeEnabled();
  await expect.element(reset).toBeChecked();
  await expect.element(banked).not.toBeChecked();
  await expect.element(page.getByRole('switch', { name: 'Enable quota alerts' })).not.toBeChecked();
  await expect
    .element(
      page.getByText(
        'Notify when Codex reports available banked resets. Resets are not automatically redeemed.'
      )
    )
    .toBeVisible();
});

test('tests a saved destination while automatic notifications are disabled', async () => {
  const { client, state } = fixture({
    enabled: false,
    status: { enabled: false, configured: true, in_flight: false }
  });
  render(NotificationsPanel, { client });
  await expect.element(page.getByRole('button', { name: 'Send test notification' })).toBeEnabled();
  await page.getByRole('button', { name: 'Send test notification' }).click();
  await expect.element(page.getByText('Test notification sent.', { exact: true })).toBeVisible();
  expect(state.tests).toBe(1);
});
