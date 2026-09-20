<script lang="ts">
  import CodexDeviceAuth from '$lib/components/management/CodexDeviceAuth.svelte';
  import { onDestroy, onMount, untrack } from 'svelte';
  import { RefreshCw, LogOut, CircleHelp } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { subscribeChanges, Changes, type LiveStatus } from '$lib/realtime';
  import UsagePanel from './management/UsagePanel.svelte';
  import Overview from './Overview.svelte';
  import LogsPanel from './LogsPanel.svelte';
  import CredentialsPanel from './management/CredentialsPanel.svelte';
  import KeysPanel from './management/KeysPanel.svelte';
  import SettingsPanel from './management/SettingsPanel.svelte';
  import NotificationsPanel from './management/NotificationsPanel.svelte';
  import {
    createSessionManagementClient,
    type ManagementClient,
    type AuthFile,
    type ManagementConfig
  } from '$lib/api';
  import { demoFiles, demoKeys, demoConfig, demoClient, demoNotifications } from '$lib/demo';

  let {
    initialDemo = false,
    onlogout = () => window.location.assign('/login')
  }: { initialDemo?: boolean; onlogout?: () => void } = $props();
  type View = 'Overview' | 'Usage' | 'Credentials' | 'API keys' | 'Logs' | 'Settings';
  const views: View[] = ['Overview', 'Usage', 'Credentials', 'API keys', 'Logs', 'Settings'];
  const descriptions: Record<View, string> = {
    Overview: 'A clear view of your proxy instance.',
    Usage: 'Remaining allowances and reset times across all your accounts.',
    Credentials: 'Manage the accounts that power your proxy.',
    'API keys': 'Control which clients can access your proxy.',
    Logs: 'Inspect recent activity from your server.',
    Settings: 'Configure how your instance handles requests.'
  };
  // This prop seeds a story preview; subsequent state belongs to the session.
  const startInDemo = untrack(() => initialDemo);
  let demo = $state(startInDemo);
  let client = $state.raw<ManagementClient>(
    startInDemo ? demoClient : createSessionManagementClient(sessionFetch)
  );
  let files = $state<AuthFile[]>(startInDemo ? structuredClone(demoFiles) : []);
  let keys = $state<string[]>(startInDemo ? [...demoKeys] : []);
  let config = $state<ManagementConfig>(startInDemo ? structuredClone(demoConfig) : {});
  let view = $state<View>('Overview');
  let busy = $state(false);
  let notificationSaving = $state(false);
  let liveStatus = $state<LiveStatus>('connecting');
  let signingOut = $state(false);
  let sessionExpired = $state(false);
  let errors = $state<Record<string, string>>({});
  let updated = $state(startInDemo ? 'Preview data' : '');
  let controller: AbortController | undefined;
  let refreshPromise: Promise<void> | undefined;
  let refreshQueued = false;
  let refreshTopics = 0;
  let stopUpdates = () => {};
  let destroyed = false;
  const message = (error: unknown) =>
    error instanceof Error ? error.message : 'Unable to complete this request. Please try again.';

  async function sessionFetch(input: RequestInfo | URL, init?: RequestInit) {
    const response = await fetch(input, init);
    if (response.status === 401) {
      stopUpdates();
      clearData();
      sessionExpired = true;
    }
    return response;
  }

  function refresh(
    background = false,
    topics: number = Changes.accounts | Changes.config
  ): Promise<void> {
    if (!client || sessionExpired || destroyed) return Promise.resolve();
    if (demo) {
      updated = 'Demo data';
      return Promise.resolve();
    }
    // A completed mutation needs a snapshot started after it, even if a read is in flight.
    refreshQueued = true;
    refreshTopics |= topics;
    if (refreshPromise) return refreshPromise;
    if (!background) busy = true;
    refreshPromise = (async () => {
      try {
        do {
          refreshQueued = false;
          const topics = refreshTopics;
          refreshTopics = 0;
          await refreshOnce(topics);
        } while (refreshQueued && !sessionExpired && !destroyed);
      } finally {
        busy = false;
        refreshPromise = undefined;
      }
    })();
    return refreshPromise;
  }

  async function refreshOnce(topics: number) {
    const activeClient = client;
    controller?.abort();
    const pending = new AbortController();
    controller = pending;
    const jobs: { name: string; run: () => Promise<unknown>; apply: (value: unknown) => void }[] = [
      {
        name: 'Credentials',
        run: () => activeClient.getAuthFiles(pending.signal),
        apply: (value: unknown) => {
          files = value as AuthFile[];
        }
      },
      {
        name: 'API keys',
        run: () => activeClient.getAPIKeys(pending.signal),
        apply: (value: unknown) => {
          keys = value as string[];
        }
      },
      {
        name: 'Settings',
        run: () => activeClient.getConfig(pending.signal),
        apply: (value: unknown) => {
          config = value as ManagementConfig;
        }
      }
    ];
    const selected = jobs.filter(
      (job) => topics & (job.name === 'Credentials' ? Changes.accounts : Changes.config)
    );
    const results = await Promise.allSettled(selected.map((job) => job.run()));
    if (pending.signal.aborted || client !== activeClient) return;
    const nextErrors = { ...errors };
    results.forEach((result, index) => {
      const job = selected[index];
      if (result.status === 'fulfilled') {
        job.apply(result.value);
        delete nextErrors[job.name];
      } else {
        nextErrors[job.name] = message(result.reason);
      }
    });
    errors = nextErrors;
    updated = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  function clearData() {
    refreshQueued = false;
    controller?.abort();
    files = [];
    keys = [];
    config = {};
    errors = {};
  }
  async function signOut() {
    signingOut = true;
    try {
      const response = await fetch('/api/session', {
        method: 'DELETE',
        credentials: 'same-origin'
      });
      if (!response.ok) throw new Error('Unable to sign out. Please try again.');
      stopUpdates();
      clearData();
      sessionExpired = true;
      onlogout();
    } catch {
      errors = { ...errors, Session: 'Unable to sign out. Please try again.' };
    } finally {
      signingOut = false;
    }
  }
  onMount(() => {
    void refresh();
    if (!demo)
      stopUpdates = subscribeChanges(
        (topics) => {
          if (topics & (Changes.accounts | Changes.config)) void refresh(true, topics);
        },
        (status) => {
          const changed = liveStatus !== status;
          liveStatus = status;
          // One read on disconnect detects a revoked session even if reconnect gets 401.
          if (changed && status === 'reconnecting') void refresh(true);
        }
      );
    return () => stopUpdates();
  });
  async function navigate(next: View) {
    // Keep the notification form mounted until its save response has been applied.
    if (notificationSaving) return;
    view = next;
  }
  onDestroy(() => {
    destroyed = true;
    refreshQueued = false;
    controller?.abort();
  });
</script>

<svelte:head
  ><title>Management</title><meta
    name="description"
    content="Manage credentials, API keys, logs, and routing settings."
  /></svelte:head
>
<a class="skip-link" href="#main">Skip to content</a>
<div class="app-shell">
  <header class="topbar">
    <div class="topbar-inner">
      {#if client}
        <nav aria-label="Management sections">
          {#each views as item}<button
              class:active={view === item}
              disabled={busy || notificationSaving}
              aria-current={view === item ? 'page' : undefined}
              onclick={() => navigate(item)}>{item}</button
            >{/each}
        </nav>
      {/if}

      <div class="topbar-actions">
        {#if !demo}<Button
            variant="ghost"
            onclick={signOut}
            disabled={signingOut}
            aria-label="Sign out"
            ><LogOut size={15} />{signingOut ? 'Signing out…' : 'Sign out'}</Button
          >{/if}
      </div>
    </div>
  </header>
  <main id="main" tabindex="-1">
    {#if sessionExpired}
      <div class="panel empty-state" role="alert">
        <h1>Session ended</h1>
        <p>Sign in again to continue.</p>
        <Button href="/login">Sign in</Button>
      </div>
    {:else}
      {#if demo}<div class="demo-banner">
          <span><CircleHelp size={15} /> Preview data · Changes disabled</span>
        </div>{/if}
      <div class="page-heading">
        <div>
          <h1>{view}</h1>
          <p class="muted">{descriptions[view]}</p>
        </div>
        <div class="heading-actions">
          <span
            class:offline={demo}
            class:attention={!demo &&
              (Object.keys(errors).length > 0 || liveStatus !== 'connected')}
            class="status"
            >{demo
              ? 'Demo mode'
              : Object.keys(errors).length
                ? 'Needs attention'
                : liveStatus === 'connected'
                  ? 'Live'
                  : liveStatus === 'reconnecting'
                    ? 'Reconnecting live updates…'
                    : 'Connecting live updates…'}</span
          ><Button
            variant="outline"
            onclick={() => refresh()}
            disabled={busy}
            aria-label="Refresh data"
            ><RefreshCw size={14} class={busy ? 'animate-spin' : ''} />{busy
              ? 'Refreshing…'
              : 'Refresh'}</Button
          >
        </div>
      </div>
      {#if Object.keys(errors).length}<div class="error-stack" role="alert">
          {#each Object.entries(errors) as [section, error]}<p class="error-banner">
              <strong>{section}:</strong>
              {error} Previously loaded data may be out of date.
            </p>{/each}
        </div>{/if}
      <div class="view-content" aria-busy={busy || notificationSaving}>
        {#if !updated}<div class="panel empty-state" role="status">Loading your instance…</div>
        {:else if view === 'Overview'}
          {#if files.length === 0}<CodexDeviceAuth
              {client}
              live={!demo}
              disabled={demo || busy}
              onconnected={refresh}
            />{/if}
          <Overview
            {files}
            keyCount={keys.length}
            strategy={config.routing?.strategy}
            oncredentials={() => navigate('Credentials')}
          />
          <UsagePanel {files} />
        {:else if view === 'Usage'}
          <UsagePanel {files} />
        {:else if view === 'Credentials'}
          <CodexDeviceAuth {client} live={!demo} disabled={demo || busy} onconnected={refresh} />
          <CredentialsPanel {client} data={files} onrefresh={refresh} disabled={demo || busy} />
        {:else if view === 'API keys'}<KeysPanel
            {client}
            data={keys}
            onrefresh={refresh}
            disabled={demo || busy}
          />
        {:else if view === 'Logs'}<LogsPanel
            {client}
            {config}
            onrefresh={refresh}
            disabled={demo}
          />
        {:else}<SettingsPanel
            {client}
            data={config}
            onrefresh={refresh}
            disabled={demo || busy}
          /><NotificationsPanel
            {client}
            disabled={demo || busy}
            initialData={demo ? demoNotifications : undefined}
            onsavingchange={(saving) => (notificationSaving = saving)}
          />{/if}
      </div>
      <div class="instance-footer">
        <span>{demo ? 'Preview' : 'Instance'}</span><span
          >{demo
            ? 'Changes are disabled in demo mode'
            : updated
              ? `Last refreshed at ${updated}`
              : ''}</span
        >
      </div>
    {/if}
  </main>
</div>

<style>
  .app-shell {
    min-height: 100dvh;
    display: flex;
    flex-direction: column;
  }
  .topbar {
    background: var(--background);
    border-bottom: 1px solid var(--border);
  }
  .topbar-inner {
    max-width: 1216px;
    min-height: 64px;
    margin: 0 auto;
    padding: 0 32px;
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0 32px;
  }
  .topbar-actions {
    margin-left: auto;
    padding-block: 10px;
  }
  nav {
    display: flex;
    align-self: stretch;
    align-items: stretch;
    flex-wrap: wrap;
    gap: 0 24px;
    min-width: 0;
  }
  nav button {
    min-height: 48px;
    padding: 18px 0 16px;
    border: 0;
    border-bottom: 2px solid transparent;
    color: var(--muted-foreground);
    background: transparent;
  }
  nav button:hover:not(:disabled):not(.active) {
    color: var(--primary);
    border-bottom-color: var(--border);
  }
  nav button.active {
    color: var(--primary);
    border-bottom-color: var(--primary);
    font-weight: 600;
  }
  nav button:disabled {
    cursor: default;
    opacity: 0.6;
  }
  main {
    width: 100%;
    max-width: 1216px;
    margin: 0 auto;
    padding: 40px 32px 56px;
    flex: 1;
    min-width: 0;
  }
  .demo-banner {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 12px;
    padding-bottom: 16px;
    margin-bottom: 24px;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
    color: var(--muted-foreground);
  }
  .demo-banner > span {
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .page-heading {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: 20px;
    margin-bottom: 32px;
  }
  .page-heading p {
    margin-top: 8px;
  }
  .heading-actions {
    display: flex;
    align-items: center;
    gap: 20px;
    flex-shrink: 0;
  }
  .view-content {
    display: grid;
    gap: 32px;
  }
  .error-stack {
    display: grid;
    gap: 8px;
    margin-bottom: 20px;
  }
  .instance-footer {
    display: flex;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 26px;
    font-size: 12px;
    color: var(--muted-foreground);
    overflow-wrap: anywhere;
  }
  @media (max-width: 900px) {
    .topbar-inner {
      padding: 0 20px;
      gap: 0 24px;
    }
    nav {
      gap: 0 24px;
    }
    nav button {
      padding-block: 12px;
    }
  }
  @media (max-width: 760px) {
    main {
      padding: 28px 20px 40px;
    }
    .page-heading {
      align-items: start;
      flex-direction: column;
      gap: 20px;
    }
    .heading-actions {
      width: 100%;
      justify-content: space-between;
    }
  }
  @media (max-width: 400px) {
    main,
    .topbar-inner {
      padding-inline: 16px;
    }
    nav {
      column-gap: 20px;
    }
  }
</style>
