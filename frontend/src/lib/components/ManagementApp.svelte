<script lang="ts">
  import { isCodex } from '$lib/codex';
  import RequestChart from './RequestChart.svelte';
  import { formatTime } from '$lib/datetime';
  import CodexDeviceAuth from '$lib/components/management/CodexDeviceAuth.svelte';
  import { onDestroy, onMount, untrack } from 'svelte';
  import {
    RefreshCw,
    LogOut,
    PanelLeftClose,
    PanelLeftOpen,
    LayoutDashboard,
    Gauge,
    KeyRound,
    ShieldCheck,
    ScrollText,
    SlidersHorizontal
  } from '@lucide/svelte';
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
  import {
    demoSubscriptions,
    demoKeys,
    demoConfig,
    demoClient,
    demoNotifications
  } from '$lib/demo';

  let {
    initialDemo = false,
    onlogout = () => window.location.assign('/login')
  }: { initialDemo?: boolean; onlogout?: () => void } = $props();
  type View = 'Overview' | 'Usage' | 'Credentials' | 'API keys' | 'Logs' | 'Settings';
  const groups: { label: string; items: View[] }[] = [
    { label: 'Workspace', items: ['Overview'] },
    { label: 'Gateway', items: ['Credentials', 'API keys'] },
    { label: 'Observe', items: ['Usage', 'Logs'] },
    { label: 'Control', items: ['Settings'] }
  ];
  const icons = {
    Overview: LayoutDashboard,
    Usage: Gauge,
    Credentials: ShieldCheck,
    'API keys': KeyRound,
    Logs: ScrollText,
    Settings: SlidersHorizontal
  };
  // This prop seeds a story preview; subsequent state belongs to the session.
  const startInDemo = untrack(() => initialDemo);
  let demo = $state(startInDemo);
  let client = $state.raw<ManagementClient>(
    startInDemo ? demoClient : createSessionManagementClient(sessionFetch)
  );
  let files = $state<AuthFile[]>(startInDemo ? structuredClone(demoSubscriptions) : []);
  const codexFiles = $derived(files.filter(isCodex));
  let keys = $state<string[]>(startInDemo ? [...demoKeys] : []);
  let config = $state<ManagementConfig>(startInDemo ? structuredClone(demoConfig) : {});
  let view = $state<View>('Overview');
  let sidebarCollapsed = $state(false);
  const navigationID = $props.id();
  let busy = $state(false);
  let notificationSaving = $state(false);
  let liveStatus = $state<LiveStatus>('connecting');
  let signingOut = $state(false);
  let sessionExpired = $state(false);
  let errors = $state<Record<string, string>>({});
  let updated = $state(startInDemo ? formatTime() : '');
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
      updated = formatTime();
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
    updated = formatTime();
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
<div class="app-shell" class:collapsed={sidebarCollapsed} class:logs-view={view === 'Logs'}>
  <aside class="sidebar">
    <div class="sidebar-header">
      <Button
        variant="ghost"
        size="icon-lg"
        aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        aria-expanded={!sidebarCollapsed}
        aria-controls={navigationID}
        onclick={() => (sidebarCollapsed = !sidebarCollapsed)}
      >
        {#if sidebarCollapsed}<PanelLeftOpen />{:else}<PanelLeftClose />{/if}
      </Button>
    </div>
    <nav id={navigationID} aria-label="Management sections">
      {#each groups as group}
        <div class="nav-group">
          <p>{group.label}</p>
          {#each group.items as item}
            {@const Icon = icons[item]}
            <button
              class:active={view === item}
              disabled={busy || notificationSaving}
              aria-label={item === 'Usage' ? 'Quota management' : item}
              title={sidebarCollapsed ? (item === 'Usage' ? 'Quota management' : item) : undefined}
              aria-current={view === item ? 'page' : undefined}
              onclick={() => navigate(item)}
              ><Icon size={17} /><span class="sidebar-label"
                >{item === 'Usage' ? 'Quota management' : item}</span
              >
              {#if item === 'Credentials'}<span class="nav-count">{files.length}</span>{/if}
            </button>
          {/each}
        </div>
      {/each}
    </nav>
    <div class="sidebar-footer">
      {#if !demo}<Button
          variant="ghost"
          onclick={signOut}
          disabled={signingOut}
          aria-label="Sign out"
          title={sidebarCollapsed ? 'Sign out' : undefined}
          ><LogOut size={15} /><span class="sidebar-label"
            >{signingOut ? 'Signing out…' : 'Sign out'}</span
          ></Button
        >{/if}
    </div>
  </aside>
  <main id="main" tabindex="-1">
    {#if sessionExpired}
      <div class="panel empty-state" role="alert">
        <h1>Session ended</h1>
        <p>Sign in again to continue.</p>
        <Button href="/login">Sign in</Button>
      </div>
    {:else}
      <div class="page-heading">
        <div>
          <p class="eyebrow">WORKSPACE / {view === 'Usage' ? 'QUOTAS' : view.toUpperCase()}</p>
          <h1>{view === 'Usage' ? 'Quota management' : view}</h1>
        </div>
        <div class="heading-actions">
          <span
            class:error={!demo && Object.keys(errors).length > 0}
            class:attention={!demo && !Object.keys(errors).length && liveStatus === 'reconnecting'}
            class:connecting={!demo && !Object.keys(errors).length && liveStatus === 'connecting'}
            class="status"
            >{demo
              ? 'Live'
              : Object.keys(errors).length
                ? 'Needs attention'
                : liveStatus === 'connected'
                  ? 'Live'
                  : liveStatus === 'reconnecting'
                    ? 'Reconnecting live updates…'
                    : 'Connecting live updates…'}</span
          ><Button
            variant="ghost"
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
          {#if codexFiles.length}
            <UsagePanel {files} onaccounts={() => navigate('Credentials')} />
            <RequestChart files={codexFiles} title="Codex request activity" />
          {:else}
            <Overview
              {files}
              keyCount={keys.length}
              strategy={config.routing?.strategy}
              oncredentials={() => navigate('Credentials')}
            />
            <UsagePanel {files} />
          {/if}
        {:else if view === 'Usage'}
          <UsagePanel {files} />
        {:else if view === 'Credentials'}
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
        <span>Instance</span><span>{updated ? `Last refreshed at ${updated}` : ''}</span>
      </div>
    {/if}
  </main>
</div>

<style>
  .app-shell {
    min-height: 100dvh;
    display: grid;
    grid-template-columns: 232px minmax(0, 1fr);
  }
  .app-shell.logs-view {
    height: 100dvh;
    min-height: 0;
  }
  .logs-view main {
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: auto;
  }
  .logs-view .view-content {
    --logs-height: 100%;
    flex: 1;
    min-height: 128px;
    overflow: auto;
    display: flex;
    flex-direction: column;
  }
  .logs-view .page-heading,
  .logs-view .instance-footer,
  .logs-view .error-stack {
    flex-shrink: 0;
  }
  .sidebar {
    overflow: auto;
    min-width: 0;
    position: sticky;
    top: 0;
    height: 100dvh;
    display: flex;
    flex-direction: column;
    padding: 30px 16px 20px;
    border-right: 1px solid var(--border);
    background: var(--background);
  }
  .sidebar > * {
    flex-shrink: 0;
  }
  .sidebar-header {
    display: flex;
    align-items: center;
    padding-bottom: 28px;
  }
  .collapsed .sidebar-label,
  .collapsed .nav-group > p,
  .collapsed .nav-count {
    display: none;
  }
  .collapsed nav button {
    width: 42px;
    justify-content: center;
    padding-inline: 0;
  }
  .collapsed nav {
    gap: 4px;
  }
  .collapsed .sidebar-header {
    padding-bottom: 12px;
  }
  .collapsed .nav-group {
    display: contents;
  }
  .collapsed .sidebar-footer {
    padding-inline: 0;
  }
  @media (min-width: 761px) {
    .app-shell.collapsed {
      grid-template-columns: 72px minmax(0, 1fr);
    }
    .collapsed .sidebar {
      padding-inline: 14px;
    }
    .collapsed .sidebar-header {
      justify-content: center;
    }
  }
  nav {
    min-width: 0;
    display: grid;
    gap: 26px;
  }
  .nav-group {
    min-width: 0;
  }
  nav button > span {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  nav button :global(svg) {
    flex-shrink: 0;
  }
  .nav-group > p {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 1.5px;
    color: var(--muted-foreground);
    padding: 0 12px 9px;
  }
  nav button {
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 11px;
    width: 100%;
    min-height: 42px;
    padding: 10px 12px;
    border: 1px solid transparent;
    border-radius: 7px;
    color: var(--muted-foreground);
    background: transparent;
    text-align: left;
    font-size: 13px;
  }
  nav button:hover:not(:disabled),
  nav button.active {
    color: var(--foreground);
    background: var(--muted);
  }
  nav button.active {
    font-weight: 600;
  }
  nav button:disabled {
    opacity: 0.6;
  }
  .nav-count {
    margin-left: auto;
    font-size: 11px;
    border-radius: 5px;
    background: var(--border);
    padding: 0 6px;
  }
  .sidebar-footer {
    margin-top: auto;
    display: grid;
    gap: 16px;
    padding: 20px 10px 0;
  }
  main {
    width: 100%;
    max-width: 1600px;
    margin: 0 auto;
    padding: 48px 48px 32px;
    min-width: 0;
  }
  .eyebrow {
    font-size: 10px;
    letter-spacing: 1.5px;
    color: var(--muted-foreground);
    margin-bottom: 18px;
  }
  .page-heading {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: 20px;
    margin-bottom: 32px;
  }
  .page-heading > div {
    min-width: 0;
    max-width: 100%;
    overflow-wrap: anywhere;
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
  @media (max-width: 1100px) {
    main {
      padding: 32px 24px;
    }
    .app-shell {
      grid-template-columns: 212px minmax(0, 1fr);
    }
  }
  @media (max-width: 760px) {
    .app-shell.logs-view {
      display: flex;
      flex-direction: column;
    }
    .logs-view .sidebar {
      flex-shrink: 0;
      max-height: 40dvh;
    }
    .logs-view main {
      flex: 1;
      padding-block: 16px;
    }
    .logs-view .page-heading {
      margin-bottom: 8px;
      gap: 8px;
    }
    .logs-view .eyebrow {
      display: none;
    }
    .logs-view .instance-footer {
      margin-top: 8px;
    }
    .app-shell {
      display: block;
    }
    .sidebar {
      overflow: auto;
      min-width: 0;
      position: static;
      height: auto;
      padding: 18px 16px 12px;
      border-right: 0;
      border-bottom: 1px solid var(--border);
    }
    .sidebar > * {
      flex-shrink: 0;
    }
    .sidebar-header {
      padding-bottom: 12px;
    }
    .collapsed nav {
      gap: 4px;
    }
    nav {
      min-width: 0;
      display: flex;
      flex-wrap: wrap;
      gap: 4px;
    }
    .nav-group {
      display: contents;
    }
    .nav-group > p,
    .nav-count {
      display: none;
    }
    .sidebar-footer {
      padding: 8px 0 0;
    }
    nav button {
      min-width: 0;
      width: auto;
      min-height: 40px;
      padding: 8px;
      font-size: 12px;
    }
    main {
      padding: 28px 16px;
    }
    .page-heading {
      align-items: start;
    }
    .heading-actions {
      width: 100%;
      justify-content: space-between;
    }
  }
</style>
