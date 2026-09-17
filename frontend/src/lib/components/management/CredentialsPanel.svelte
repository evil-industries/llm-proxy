<script lang="ts">
  import { Search, Upload, Trash2, KeyRound } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import type { AuthFile, ManagementClient } from '$lib/api';

  let {
    client,
    data,
    onrefresh,
    disabled = false
  }: {
    client: ManagementClient;
    data: AuthFile[];
    onrefresh: () => Promise<void>;
    disabled?: boolean;
  } = $props();
  let search = $state('');
  let provider = $state('all');
  let busy = $state(false);
  let revision = $state(0);
  let error = $state('');
  let success = $state('');
  let deleting = $state<string | null>(null);
  let uploadInput: HTMLInputElement;
  const providers = $derived(
    [...new Set(data.map((file) => file.provider || file.type || 'Unknown'))].sort()
  );
  const filtered = $derived(
    data.filter((file) => {
      const matchesProvider =
        provider === 'all' || (file.provider || file.type || 'Unknown') === provider;
      return (
        matchesProvider &&
        `${file.name} ${file.email ?? ''} ${file.provider ?? file.type ?? ''}`
          .toLowerCase()
          .includes(search.toLowerCase())
      );
    })
  );

  async function mutate(action: () => Promise<unknown>, message: string) {
    busy = true;
    error = '';
    success = '';
    try {
      await action();
      await onrefresh();
      success = message;
      deleting = null;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The credential could not be updated.';
    } finally {
      busy = false;
      revision += 1;
    }
  }
  async function upload(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    await mutate(() => client.uploadAuthFile(file), 'Credential uploaded.');
    input.value = '';
  }
</script>

<section class="panel" aria-labelledby="credentials-title">
  <div class="panel-header">
    <div class="section-heading">
      <h2 id="credentials-title">Provider credentials</h2>
      <p class="muted">Manage the accounts available to your proxy.</p>
    </div>
    <Button disabled={disabled || busy} onclick={() => uploadInput.click()}
      ><Upload size={15} /> Upload JSON</Button
    >
    <input
      bind:this={uploadInput}
      type="file"
      accept=".json,application/json"
      aria-label="Upload credential JSON"
      class="sr-only"
      tabindex="-1"
      disabled={disabled || busy}
      onchange={upload}
    />
  </div>
  <div class="credential-toolbar">
    <div class="search-field">
      <Search size={16} aria-hidden="true" /><Input
        aria-label="Search credentials"
        placeholder="Search credentials…"
        bind:value={search}
      />
    </div>
    <select aria-label="Filter by provider" bind:value={provider}
      ><option value="all">All providers</option>{#each providers as item}<option value={item}
          >{item}</option
        >{/each}</select
    >
    <span class="muted result-count"
      >{filtered.length}
      {filtered.length === 1 ? 'credential' : 'credentials'}</span
    >
  </div>
  {#if error}<div class="error-banner" role="alert">{error}</div>{/if}
  {#if success}<div class="success-banner" role="status">{success}</div>{/if}
  {#if filtered.length === 0}
    <div class="empty-state">
      <KeyRound size={24} />
      <h3>{data.length ? 'No matching credentials' : 'No credentials yet'}</h3>
      <p>
        {data.length
          ? 'Try another search or provider.'
          : 'Upload a provider credential JSON file to start routing requests.'}
      </p>
    </div>
  {:else}
    <ul class="credential-list">
      {#each filtered as file (file.auth_index || file.name)}
        <li class="credential-row">
          <div class="credential-summary">
            <div class="credential-name">{file.name}</div>
            <div class="credential-meta">
              <span>{file.provider || file.type || 'Unknown'}</span>{#if file.email}<span
                  >{file.email}</span
                >{/if}
            </div>
            {#if file.runtime_only}<p class="status-message muted">
                Runtime credential · No saved file
              </p>{/if}
            {#if file.status_message}<p class="status-message muted">
                {file.status_message}
              </p>{/if}
          </div>
          <div class="credential-controls">
            <Badge variant="outline"
              >{file.disabled
                ? 'Disabled'
                : file.unavailable
                  ? 'Unavailable'
                  : file.status === 'error'
                    ? 'Error'
                    : 'Active'}</Badge
            >{#key revision}<Switch
                checked={!file.disabled}
                disabled={disabled || busy}
                aria-label={`Enable ${file.name}`}
                onCheckedChange={(checked) =>
                  mutate(
                    () => client.setAuthFileDisabled(file.name, !checked, file.auth_index),
                    checked ? 'Credential enabled.' : 'Credential disabled.'
                  )}
              />{/key}{#if !file.runtime_only}<Button
                variant="ghost"
                size="icon"
                aria-label={`Delete ${file.name}`}
                disabled={disabled || busy}
                onclick={() => (deleting = file.auth_index || file.name)}
                ><Trash2 size={15} /></Button
              >{/if}
          </div>
          {#if !file.runtime_only && deleting === (file.auth_index || file.name)}<div
              class="delete-confirmation"
            >
              <p>
                Delete <strong>{file.name}</strong>? This removes its saved credentials.
              </p>
              <div class="row-actions">
                <Button variant="outline" disabled={busy} onclick={() => (deleting = null)}
                  >Cancel</Button
                ><Button
                  variant="destructive"
                  disabled={busy}
                  onclick={() =>
                    mutate(() => client.deleteAuthFile(file.name), 'Credential deleted.')}
                  >{busy ? 'Deleting…' : 'Delete credential'}</Button
                >
              </div>
            </div>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .credential-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
    padding: 18px 24px;
    border-bottom: 1px solid var(--border);
  }
  .search-field {
    position: relative;
    flex: 1 1 200px;
    min-width: 0;
    max-width: 340px;
  }
  .search-field :global(svg) {
    position: absolute;
    top: 9px;
    left: 10px;
    color: var(--muted-foreground);
  }
  .search-field :global(input) {
    padding-left: 34px;
  }
  select {
    min-width: 0;
    max-width: 100%;
    padding: 6px 28px 6px 10px;
    border: 1px solid var(--border);
    border-radius: 7px;
    background: var(--background);
    font-size: 13px;
  }
  .result-count {
    margin-left: auto;
    font-size: 12px;
  }
  .credential-list {
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .credential-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 18px;
    padding: 20px 24px;
    border-bottom: 1px solid var(--border);
  }
  .credential-row:last-child {
    border-bottom: 0;
  }
  .credential-summary {
    flex: 1 1 200px;
    min-width: 0;
  }
  .credential-name {
    font-weight: 500;
    overflow-wrap: anywhere;
  }
  .credential-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    margin-top: 5px;
    color: var(--muted-foreground);
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .credential-meta span {
    min-width: 0;
  }
  .status-message {
    font-size: 12px;
    overflow-wrap: anywhere;
    margin-top: 8px;
  }
  .credential-controls {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 15px;
  }
  .delete-confirmation {
    flex-basis: 100%;
    padding: 14px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--muted);
  }
  .delete-confirmation p {
    overflow-wrap: anywhere;
    margin-bottom: 12px;
  }
  @media (max-width: 480px) {
    .credential-row,
    .credential-toolbar {
      padding: 16px;
    }
    .search-field {
      max-width: none;
    }
    .result-count {
      margin-left: 0;
    }
  }
</style>
