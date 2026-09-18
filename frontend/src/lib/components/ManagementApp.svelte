<script lang="ts">
  import CodexDeviceAuth from '$lib/components/management/CodexDeviceAuth.svelte';
  import { onDestroy, onMount, untrack } from 'svelte';
  import { RefreshCw, LogOut, CircleHelp } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
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
  type View = 'Overview' | 'Credentials' | 'API keys' | 'Logs' | 'Settings';
  const views: View[] = ['Overview', 'Credentials', 'API keys', 'Logs', 'Settings'];
  const descriptions: Record<View, string> = {
    Overview: 'A clear view of your proxy instance.',
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
  let signingOut = $state(false);
  let sessionExpired = $state(false);
  let errors = $state<Record<string, string>>({});
  let updated = $state(startInDemo ? 'Preview data' : '');
  let controller: AbortController | undefined;
  let refreshPromise: Promise<void> | undefined;
  let refreshQueued = false;
  let destroyed = false;
  const message = (error: unknown) =>
    error instanceof Error ? error.message : 'Unable to complete this request. Please try again.';

  async function sessionFetch(input: RequestInfo | URL, init?: RequestInit) {
    const response = await fetch(input, init);
    if (response.status === 401) {
      clearData();
      sessionExpired = true;
    }
    return response;
  }

  function refresh(): Promise<void> {
    if (!client || sessionExpired || destroyed) return Promise.resolve();
    if (demo) {
      updated = 'Demo data';
      return Promise.resolve();
    }
    // A completed mutation needs a snapshot started after it, even if a read is in flight.
    refreshQueued = true;
    if (refreshPromise) return refreshPromise;
    busy = true;
    refreshPromise = (async () => {
      try {
        do {
          refreshQueued = false;
          await refreshOnce();
        } while (refreshQueued && !sessionExpired && !destroyed);
      } finally {
        busy = false;
        refreshPromise = undefined;
      }
    })();
    return refreshPromise;
  }

  async function refreshOnce() {
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
    const results = await Promise.allSettled(jobs.map((job) => job.run()));
    if (pending.signal.aborted || client !== activeClient) return;
    const nextErrors = { ...errors };
    results.forEach((result, index) => {
      const job = jobs[index];
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
      <span class="workspace">Management</span>
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
  {#if client}
    <div class="navigation">
      <nav aria-label="Management sections">
        {#each views as item}<button
            class:active={view === item}
            disabled={busy || notificationSaving}
            aria-current={view === item ? 'page' : undefined}
            onclick={() => navigate(item)}>{item}</button
          >{/each}
      </nav>
    </div>
  {/if}
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
          <div class="eyebrow">{demo ? 'PREVIEW' : 'INSTANCE'}</div>
          <h1>{view}</h1>
          <p class="muted">{descriptions[view]}</p>
        </div>
        <div class="heading-actions">
          <span class:offline={demo || Object.keys(errors).length > 0} class="status"
            >{demo
              ? 'Demo mode'
              : Object.keys(errors).length
                ? 'Needs attention'
                : 'Connected'}</span
          ><Button variant="outline" onclick={refresh} disabled={busy} aria-label="Refresh data"
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
              disabled={demo || busy}
              onconnected={refresh}
            />{/if}
          <Overview
            {files}
            keyCount={keys.length}
            strategy={config.routing?.strategy}
            oncredentials={() => navigate('Credentials')}
          />
        {:else if view === 'Credentials'}
          <CodexDeviceAuth {client} disabled={demo || busy} onconnected={refresh} />
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
    background: #fff;
    border-bottom: 1px solid #eaeaea;
  }
  .topbar-inner {
    min-height: 72px;
    padding: 14px 32px;
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 20px;
  }
  .workspace {
    font-weight: 500;
  }
  .topbar-actions {
    margin-left: auto;
    display: flex;
    flex-wrap: wrap;
    max-width: 100%;
    align-items: center;
    gap: 24px;
  }
  .navigation {
    background: white;
    border-bottom: 1px solid #e5e5e5;
  }
  nav {
    max-width: 1216px;
    margin: 0 auto;
    padding: 0 32px;
    display: flex;
    gap: 26px;
    flex-wrap: wrap;
  }
  nav button {
    padding: 17px 0 15px;
    border: 0;
    border-bottom: 2px solid transparent;
    color: #666;
    background: transparent;
  }
  nav button.active {
    color: #171717;
    border-bottom-color: #171717;
    font-weight: 500;
  }
  main {
    width: 100%;
    max-width: 1216px;
    margin: 0 auto;
    padding: 36px 32px 56px;
    flex: 1;
    min-width: 0;
  }
  .demo-banner {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-wrap: wrap;
    gap: 12px;
    border: 1px solid #e5e5e5;
    background: #f5f5f5;
    padding: 11px 16px;
    border-radius: 7px;
    margin-bottom: 32px;
    font-size: 12px;
    color: #555;
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
    margin-bottom: 30px;
  }
  .eyebrow {
    font-size: 10px;
    letter-spacing: 1.3px;
    color: #737373;
    margin-bottom: 10px;
    font-weight: 500;
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
    color: #737373;
    overflow-wrap: anywhere;
  }
  @media (max-width: 760px) {
    .topbar-inner {
      padding: 16px 20px;
      gap: 12px;
      flex-wrap: wrap;
    }
    .topbar-actions {
      gap: 12px;
    }
    nav {
      gap: 22px;
      padding: 0 20px;
    }
    main {
      padding: 24px 20px 40px;
    }
    .page-heading {
      align-items: start;
      flex-direction: column;
    }
    .heading-actions {
      width: 100%;
      justify-content: space-between;
    }
    .demo-banner {
      margin-bottom: 26px;
    }
  }
  @media (max-width: 400px) {
    main {
      padding-inline: 16px;
    }
    nav {
      column-gap: 20px;
      row-gap: 0;
    }
    nav button {
      padding-block: 12px;
    }
  }
</style>
