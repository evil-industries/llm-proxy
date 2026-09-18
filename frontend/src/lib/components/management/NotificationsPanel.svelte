<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { Bell, Save, Send, RefreshCw } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import type { ManagementClient, NotificationSettings, NotificationUpdate } from '$lib/api';

  let {
    client,
    disabled = false,
    initialData,
    onsavingchange = () => {}
  }: {
    client: ManagementClient;
    disabled?: boolean;
    initialData?: NotificationSettings;
    onsavingchange?: (saving: boolean) => void;
  } = $props();
  const initial = untrack(() => initialData);
  let saved = $state<NotificationSettings | undefined>(initial);
  let enabled = $state(initial?.enabled ?? false);
  let resetNotifications = $state(initial?.reset_notifications ?? true);
  let bankedResetNotifications = $state(initial?.banked_reset_notifications ?? true);
  let url = $state(initial?.url ?? '');
  let topic = $state(initial?.topic ?? '');
  let warning = $state<number | undefined>(initial?.warning_percent ?? 20);
  let critical = $state<number | undefined>(initial?.critical_percent ?? 5);
  let token = $state('');
  let clearToken = $state(false);
  let busy = $state<'load' | 'save' | 'test' | null>(null);
  let error = $state('');
  let success = $state('');
  const dirty = $derived(
    !!saved &&
      (enabled !== saved.enabled ||
        resetNotifications !== saved.reset_notifications ||
        bankedResetNotifications !== saved.banked_reset_notifications ||
        url !== saved.url ||
        topic !== saved.topic ||
        warning !== saved.warning_percent ||
        critical !== saved.critical_percent ||
        token !== '' ||
        clearToken)
  );
  const locked = $derived(disabled || busy !== null);
  const message = (cause: unknown) =>
    cause instanceof Error ? cause.message : 'Unable to update notifications. Please try again.';

  function apply(data: NotificationSettings) {
    saved = data;
    enabled = data.enabled;
    resetNotifications = data.reset_notifications;
    bankedResetNotifications = data.banked_reset_notifications;
    url = data.url;
    topic = data.topic;
    warning = data.warning_percent;
    critical = data.critical_percent;
    token = '';
    clearToken = false;
  }
  async function load(signal?: AbortSignal) {
    busy = 'load';
    error = '';
    success = '';
    try {
      apply(await client.getNotifications(signal));
    } catch (cause) {
      if (!signal?.aborted) error = message(cause);
    } finally {
      busy = null;
    }
  }
  onMount(() => {
    const controller = new AbortController();
    if (!saved) void load(controller.signal);
    return () => {
      controller.abort();
      onsavingchange(false);
    };
  });
  async function save(event: SubmitEvent) {
    event.preventDefault();
    error = '';
    success = '';
    if (
      warning === undefined ||
      critical === undefined ||
      !Number.isFinite(warning) ||
      !Number.isFinite(critical) ||
      critical < 0 ||
      warning > 100 ||
      critical >= warning
    ) {
      error = 'Critical quota must be lower than warning quota, between 0% and 100%.';
      return;
    }
    const update: NotificationUpdate = {
      enabled,
      reset_notifications: resetNotifications,
      banked_reset_notifications: bankedResetNotifications,
      url: url.trim(),
      topic: topic.trim(),
      warning_percent: warning,
      critical_percent: critical,
      ...(clearToken ? { clear_token: true } : token ? { token } : {})
    };
    busy = 'save';
    onsavingchange(true);
    try {
      apply(await client.setNotifications(update));
      success = 'Notification settings saved.';
    } catch (cause) {
      error = message(cause);
    } finally {
      busy = null;
      onsavingchange(false);
    }
  }
  async function testDelivery() {
    if (dirty || locked || !saved?.status.configured) return;
    busy = 'test';
    error = '';
    success = '';
    try {
      await client.testNotifications();
      success = 'Test notification sent.';
      // Refresh delivery status without turning a delivered test into a reported failure.
      try {
        apply(await client.getNotifications());
      } catch {
        /* Keep the last saved settings. */
      }
    } catch (cause) {
      error = message(cause);
    } finally {
      busy = null;
    }
  }
</script>

<section class="panel notifications-panel" aria-labelledby="notifications-title">
  <div class="panel-header">
    <div class="section-heading">
      <h2 id="notifications-title"><Bell size={16} /> Quota notifications</h2>
      <p class="muted">Send quota alerts and reset notifications to your ntfy server.</p>
    </div>
    {#if saved}<Button
        variant="outline"
        aria-label="Refresh notification status"
        disabled={locked || dirty}
        onclick={() => load()}><RefreshCw size={14} />Refresh status</Button
      >{/if}
  </div>
  {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
  {#if success}<p class="success-banner" role="status">{success}</p>{/if}
  {#if !saved}
    <div class="empty-state">
      {#if busy === 'load'}<p role="status">Loading notification settings…</p>
      {:else}<Button variant="outline" onclick={() => load()} {disabled}>Retry notifications</Button
        >{/if}
    </div>
  {:else}
    <form onsubmit={save}>
      <div class="notification-enable">
        <div>
          <label for="notifications-enabled">Enable quota alerts</label>
          <p class="muted">
            Alerts use provider-reported quota. Providers without quota observations cannot trigger
            alerts.
          </p>
        </div>
        <Switch id="notifications-enabled" bind:checked={enabled} disabled={locked} />
      </div>
      <div class="notification-enable">
        <div>
          <label for="notifications-reset">Quota reset notifications</label>
          <p id="notifications-reset-description" class="muted">
            Notify when a fresh provider observation confirms a quota reset or recovery.
          </p>
        </div>
        <Switch
          id="notifications-reset"
          aria-describedby="notifications-reset-description"
          bind:checked={resetNotifications}
          disabled={locked}
        />
      </div>
      <div class="notification-enable">
        <div>
          <label for="notifications-banked-reset">Codex banked reset notifications</label>
          <p id="notifications-banked-reset-description" class="muted">
            Notify when Codex reports available banked resets. Resets are not automatically
            redeemed.
          </p>
        </div>
        <Switch
          id="notifications-banked-reset"
          aria-describedby="notifications-banked-reset-description"
          bind:checked={bankedResetNotifications}
          disabled={locked}
        />
      </div>
      <div class="notification-fields">
        <div class="field">
          <label for="notification-url">ntfy server URL</label>
          <Input
            id="notification-url"
            type="url"
            placeholder="https://ntfy.example.com"
            bind:value={url}
            required={enabled}
            disabled={locked}
          />
        </div>
        <div class="field">
          <label for="notification-topic">Topic</label>
          <Input
            id="notification-topic"
            placeholder="quota-alerts"
            pattern={'[A-Za-z0-9_\\-]+'}
            bind:value={topic}
            required={enabled}
            disabled={locked}
          />
          <p class="muted">Letters, numbers, underscores, and hyphens.</p>
        </div>
        <div class="field">
          <label for="notification-warning">Warning remaining quota (%)</label>
          <Input
            id="notification-warning"
            type="number"
            min="0"
            max="100"
            step="any"
            bind:value={warning}
            required
            disabled={locked}
          />
        </div>
        <div class="field">
          <label for="notification-critical">Critical remaining quota (%)</label>
          <Input
            id="notification-critical"
            type="number"
            min="0"
            max="100"
            step="any"
            bind:value={critical}
            required
            disabled={locked}
          />
        </div>
        <div class="field token-field">
          <label for="notification-token">Bearer token (optional)</label>
          <Input
            id="notification-token"
            type="password"
            autocomplete="new-password"
            placeholder={saved.token_configured
              ? 'Leave blank to keep the saved token'
              : 'Enter a token if your server requires one'}
            bind:value={token}
            disabled={locked || clearToken}
          />
          <p class="muted">
            {saved.token_configured
              ? 'A token is configured. Its value is never displayed.'
              : 'No token is configured.'}
          </p>
          {#if saved.token_configured}<label class="clear-token"
              ><input type="checkbox" bind:checked={clearToken} disabled={locked} /> Remove saved token</label
            >{/if}
        </div>
      </div>
      <div class="notification-actions">
        <Button type="submit" disabled={locked || !dirty}
          ><Save size={14} />{busy === 'save' ? 'Saving…' : 'Save notifications'}</Button
        >
        <Button
          type="button"
          variant="outline"
          disabled={locked || dirty || !saved.status.configured}
          onclick={testDelivery}
          ><Send size={14} />{busy === 'test' ? 'Sending…' : 'Send test notification'}</Button
        >
        <p class="muted">
          {dirty
            ? 'Save changes before sending a test.'
            : 'Tests use your saved destination and token.'}
        </p>
      </div>
    </form>
    <div class="delivery-status" aria-label="Notification delivery status">
      <p>
        {saved.status.in_flight
          ? 'Delivery in progress.'
          : saved.enabled
            ? 'Automatic quota alerts enabled.'
            : 'Automatic quota alerts disabled.'}
      </p>
      {#if saved.status.last_success_at}<p class="muted">
          Last delivered: {new Date(saved.status.last_success_at).toLocaleString()}
        </p>{/if}
      {#if saved.status.last_error}<p role="status">
          Last delivery failed: {saved.status.last_error}
        </p>{/if}
      {#if saved.status.next_retry_at}<p class="muted">
          Next retry: {new Date(saved.status.next_retry_at).toLocaleString()}
        </p>{/if}
    </div>
  {/if}
</section>

<style>
  .notifications-panel {
    margin-top: 24px;
  }
  h2 {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .notification-enable,
  .notification-fields,
  .notification-actions,
  .delivery-status {
    padding: 24px;
  }
  .notification-enable {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
    border-bottom: 1px solid var(--border);
  }
  .notification-enable p {
    margin-top: 6px;
    max-width: 620px;
  }
  label {
    font-size: 13px;
    font-weight: 500;
  }
  .muted {
    font-size: 12px;
    line-height: 1.6;
  }
  .notification-fields {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 20px;
  }
  .field {
    display: grid;
    min-width: 0;
    gap: 8px;
  }
  .token-field {
    grid-column: 1 / -1;
  }
  .clear-token {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .notification-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
    padding-top: 0;
  }
  .notification-actions p {
    flex-basis: 100%;
  }
  .delivery-status {
    border-top: 1px solid var(--border);
    display: grid;
    gap: 6px;
    overflow-wrap: anywhere;
  }
  @media (max-width: 640px) {
    .notification-fields {
      grid-template-columns: minmax(0, 1fr);
    }
    .notification-enable,
    .notification-fields,
    .notification-actions,
    .delivery-status {
      padding: 18px 16px;
    }
    .notification-actions {
      padding-top: 0;
    }
  }
</style>
