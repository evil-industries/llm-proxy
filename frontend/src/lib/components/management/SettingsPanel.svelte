<script lang="ts">
  import { Save, Shuffle } from '@lucide/svelte';
  import { untrack } from 'svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import type {
    ManagementClient,
    ManagementConfig,
    BooleanSetting,
    RoutingStrategy
  } from '$lib/api';

  let {
    client,
    data,
    onrefresh,
    disabled = false
  }: {
    client: ManagementClient;
    data: ManagementConfig;
    onrefresh: () => Promise<void>;
    disabled?: boolean;
  } = $props();
  type SettingKey = Extract<
    BooleanSetting,
    'debug' | 'logging-to-file' | 'usage-statistics-enabled' | 'request-log' | 'ws-auth'
  >;
  const settings: { key: SettingKey; title: string; description: string }[] = [
    {
      key: 'usage-statistics-enabled',
      title: 'Usage statistics',
      description: 'Collect request counts and token usage for monitoring.'
    },
    {
      key: 'logging-to-file',
      title: 'File logging',
      description: 'Write server logs to files in the configured log directory.'
    },
    {
      key: 'request-log',
      title: 'Request logging',
      description: 'Record detailed request and response logs. These may contain sensitive content.'
    },
    {
      key: 'debug',
      title: 'Debug mode',
      description: 'Include additional diagnostic details in server logs.'
    },
    {
      key: 'ws-auth',
      title: 'WebSocket authentication',
      description: 'Require an API key for WebSocket connections.'
    }
  ];
  let routing = $state<RoutingStrategy>('round-robin');
  let values = $state<Record<SettingKey, boolean>>({
    debug: false,
    'logging-to-file': false,
    'usage-statistics-enabled': false,
    'request-log': false,
    'ws-auth': false
  });
  let busy = $state<string | null>(null);
  let error = $state('');
  let success = $state('');
  let previousValues: Record<SettingKey, boolean> | undefined;
  let previousRouting: RoutingStrategy | undefined;
  $effect(() => {
    const nextRouting = data.routing?.strategy ?? 'round-robin';
    const nextValues = Object.fromEntries(
      settings.map(({ key }) => [key, data[key] === true])
    ) as Record<SettingKey, boolean>;
    untrack(() => {
      if (previousRouting === undefined || routing === previousRouting) routing = nextRouting;
      for (const { key } of settings) {
        if (!previousValues || values[key] === previousValues[key]) values[key] = nextValues[key];
      }
      previousRouting = nextRouting;
      previousValues = nextValues;
    });
  });
  async function save(key: SettingKey | 'routing') {
    busy = key;
    error = '';
    success = '';
    try {
      if (key === 'routing') await client.setRoutingStrategy(routing);
      else await client.setBoolean(key, values[key]);
      await onrefresh();
      success =
        key === 'routing'
          ? 'Routing strategy saved.'
          : `${settings.find((setting) => setting.key === key)?.title} saved.`;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The setting could not be saved.';
    } finally {
      busy = null;
    }
  }
</script>

<section class="panel" aria-labelledby="settings-title">
  <div class="panel-header">
    <div class="section-heading">
      <h2 id="settings-title">Proxy settings</h2>
      <p class="muted">Tune routing, observability, and access.</p>
    </div>
  </div>
  {#if error}<div class="error-banner" role="alert">{error}</div>{/if}
  {#if success}<div class="success-banner" role="status">{success}</div>{/if}
  <div class="routing-row">
    <div class="setting-copy">
      <h3><Shuffle size={16} /> Request routing</h3>
      <p class="muted">Choose how requests are distributed across available credentials.</p>
    </div>
    <div class="routing-controls">
      <select
        aria-label="Routing strategy"
        bind:value={routing}
        disabled={disabled || busy !== null}
        ><option value="round-robin">Round robin</option><option value="weighted-round-robin"
          >Weighted round robin</option
        ><option value="fill-first">Fill first</option></select
      ><Button
        variant="outline"
        disabled={disabled ||
          busy !== null ||
          routing === (data.routing?.strategy ?? 'round-robin')}
        onclick={() => save('routing')}
        ><Save size={14} />
        {busy === 'routing' ? 'Saving…' : 'Save routing'}</Button
      >
    </div>
    <p class="routing-help muted">
      {routing === 'fill-first'
        ? 'Use the first available credential until it is exhausted, then move to the next.'
        : routing === 'weighted-round-robin'
          ? 'Distribute requests according to each credential’s configured weight.'
          : 'Rotate requests evenly across available credentials.'}
    </p>
  </div>
  {#each settings as setting}
    <div class="setting-row">
      <div class="setting-copy">
        <label for={`setting-${setting.key}`}>{setting.title}</label>
        <p id={`description-${setting.key}`} class="muted">
          {setting.description}
        </p>
      </div>
      <div class="setting-controls">
        <Switch
          id={`setting-${setting.key}`}
          aria-describedby={`description-${setting.key}`}
          bind:checked={values[setting.key]}
          disabled={disabled || busy !== null}
        /><Button
          variant="outline"
          disabled={disabled ||
            busy !== null ||
            values[setting.key] === (data[setting.key] === true)}
          aria-label={`Save ${setting.title.toLowerCase()}`}
          onclick={() => save(setting.key)}>{busy === setting.key ? 'Saving…' : 'Save'}</Button
        >
      </div>
    </div>
  {/each}
</section>

<style>
  .routing-row,
  .setting-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 16px 24px;
    padding: 24px;
    border-bottom: 1px solid var(--border);
  }
  .setting-row:last-child {
    border-bottom: 0;
  }
  .setting-copy {
    flex: 1 1 280px;
    min-width: 0;
  }
  .setting-copy h3,
  .setting-copy label {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 14px;
    font-weight: 500;
  }
  .setting-copy p {
    max-width: 520px;
    margin-top: 6px;
    font-size: 13px;
    line-height: 1.6;
  }
  .setting-controls,
  .routing-controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 16px;
  }
  .routing-controls {
    max-width: 100%;
    gap: 8px;
  }
  select {
    min-width: 0;
    max-width: 100%;
    padding: 6px 26px 6px 10px;
    border: 1px solid var(--border);
    border-radius: 7px;
    background: var(--background);
    font-size: 13px;
  }
  .routing-help {
    flex-basis: 100%;
    font-size: 12px;
    line-height: 1.6;
  }
  @media (max-width: 480px) {
    .routing-row,
    .setting-row {
      padding: 18px 16px;
    }
  }
</style>
