import { expect, test, vi } from 'vitest';
import { createSessionManagementClient } from './client';
import type { NotificationSettings } from './types';

const settings: NotificationSettings = {
  enabled: true,
  reset_notifications: true,
  banked_reset_notifications: true,
  url: 'https://ntfy.example.com',
  topic: 'quota-alerts',
  warning_percent: 20,
  critical_percent: 5,
  token_configured: true,
  status: { enabled: true, configured: true, in_flight: false }
};
test('notification settings keep credentials in write-only request bodies and test saved settings', async () => {
  const fetcher = vi.fn<typeof fetch>().mockImplementation(async () => Response.json(settings));
  const client = createSessionManagementClient(fetcher);
  expect(await client.getNotifications()).toEqual(settings);
  await client.setNotifications({
    enabled: true,
    reset_notifications: false,
    banked_reset_notifications: true,
    url: settings.url,
    topic: settings.topic,
    warning_percent: 25,
    critical_percent: 10,
    token: 'test-private-token'
  });
  expect(fetcher.mock.calls[1][0]).toBe('/api/management/notifications');
  expect(JSON.parse(String(fetcher.mock.calls[1][1]?.body))).toMatchObject({
    reset_notifications: false,
    banked_reset_notifications: true,
    token: 'test-private-token'
  });
  await client.testNotifications();
  expect(fetcher.mock.calls[2]).toEqual([
    '/api/management/notifications/test',
    expect.objectContaining({ method: 'POST', body: '{}' })
  ]);
});
test.each([
  {},
  { ...settings, token_configured: 'yes' },
  { ...settings, reset_notifications: undefined },
  { ...settings, banked_reset_notifications: 'true' },
  { ...settings, critical_percent: 21 },
  { ...settings, warning_percent: 101 },
  { ...settings, status: { configured: true } }
])('rejects malformed notification responses', async (body) => {
  const client = createSessionManagementClient(async () => Response.json(body));
  await expect(client.getNotifications()).rejects.toMatchObject({ code: 'invalid-response' });
  await expect(
    client.setNotifications({
      enabled: false,
      reset_notifications: true,
      banked_reset_notifications: true,
      url: '',
      topic: '',
      warning_percent: 20,
      critical_percent: 5
    })
  ).rejects.toMatchObject({ code: 'invalid-response' });
});
