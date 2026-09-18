<script lang="ts">
  import { untrack } from 'svelte';
  import {
    createManagementClient,
    type NotificationSettings,
    type NotificationUpdate
  } from '$lib/api';
  import { demoNotifications } from '$lib/demo';
  import NotificationsPanel from './NotificationsPanel.svelte';
  let {
    configured = false,
    failure = false,
    disabled = false,
    resetEvents = true
  }: {
    configured?: boolean;
    failure?: boolean;
    disabled?: boolean;
    resetEvents?: boolean;
  } = $props();
  const initialData: NotificationSettings = untrack(() =>
    configured
      ? {
          ...demoNotifications,
          enabled: true,
          reset_notifications: resetEvents,
          banked_reset_notifications: resetEvents,
          url: 'https://ntfy.example.com',
          topic: 'quota-alerts',
          token_configured: true,
          status: {
            enabled: true,
            configured: true,
            in_flight: false,
            last_success_at: '2026-09-17T10:00:00Z',
            ...(failure
              ? { last_error: 'The notification server could not accept the message.' }
              : {})
          }
        }
      : structuredClone(demoNotifications)
  );
  let saved = structuredClone(initialData);
  const client = createManagementClient({
    baseUrl: 'http://story.invalid',
    managementKey: 'story',
    fetch: async (input, init) => {
      if (failure && init?.method !== 'GET') return Response.json({}, { status: 502 });
      if (String(input).endsWith('/notifications/test')) return Response.json({ status: 'ok' });
      if (init?.method === 'PUT') {
        const update = JSON.parse(String(init.body)) as NotificationUpdate;
        saved = {
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
    }
  });
</script>

<div style="padding:24px;min-width:0"><NotificationsPanel {client} {initialData} {disabled} /></div>
