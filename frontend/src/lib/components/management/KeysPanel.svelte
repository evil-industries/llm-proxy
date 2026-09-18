<script lang="ts">
  import { Copy, Eye, EyeOff, KeyRound, Plus, Trash2 } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { ManagementError, type ManagementClient } from '$lib/api';

  let {
    client,
    data,
    onrefresh,
    disabled = false
  }: {
    client: ManagementClient;
    data: string[];
    onrefresh: () => Promise<void>;
    disabled?: boolean;
  } = $props();
  let newKey = $state('');
  let revealed = $state<number[]>([]);
  let deleting = $state<string | null>(null);
  let busy = $state(false);
  let error = $state('');
  let success = $state('');

  async function mutate(action: () => Promise<unknown>, message: string) {
    busy = true;
    error = '';
    success = '';
    try {
      await action();
      await onrefresh();
      success = message;
      deleting = null;
      revealed = [];
    } catch (cause) {
      error =
        cause instanceof ManagementError && cause.status === 409
          ? 'The key list changed, the key already exists, or this is the final key. Refresh and keep at least one API key configured.'
          : cause instanceof Error
            ? cause.message
            : 'The API key could not be updated.';
    } finally {
      busy = false;
    }
  }
  async function addKey(event: SubmitEvent) {
    event.preventDefault();
    const value = newKey.trim();
    if (!value) return;
    await mutate(async () => {
      await client.addAPIKey(value);
      newKey = '';
    }, 'API key added.');
  }
  async function deleteKey(value: string) {
    await mutate(() => client.removeAPIKey(value), 'API key deleted.');
  }
  function canRemove(key: string) {
    return data.some((value) => value.trim() !== '' && value.trim() !== key.trim());
  }
  async function copyKey(value: string) {
    error = '';
    success = '';
    try {
      await navigator.clipboard.writeText(value);
      success = 'API key copied.';
    } catch {
      error = 'Clipboard access is unavailable. Reveal the key to copy it manually.';
    }
  }
</script>

<section class="panel" aria-labelledby="keys-title">
  <div class="panel-header">
    <div class="section-heading">
      <h2 id="keys-title">API keys</h2>
      <p class="muted">Control which clients can access your proxy.</p>
      {#if data.length > 0 && new Set(data.map((key) => key.trim()).filter(Boolean)).size === 1}
        <p class="muted">
          Add another key before deleting the final key. An empty key list disables API
          authentication.
        </p>
      {/if}
    </div>
    <Badge variant="outline">{data.length} {data.length === 1 ? 'key' : 'keys'}</Badge>
  </div>
  <form class="add-key-form" onsubmit={addKey}>
    <div class="key-field">
      <label for="new-api-key">Add an API key</label><Input
        id="new-api-key"
        type="password"
        autocomplete="new-password"
        placeholder="Enter a strong, unique key"
        bind:value={newKey}
        disabled={disabled || busy}
        required
      />
      <p class="muted">Use this key as the bearer token in your client.</p>
    </div>
    <Button type="submit" disabled={disabled || busy || !newKey.trim()}
      ><Plus size={15} /> {busy ? 'Saving…' : 'Add key'}</Button
    >
  </form>
  {#if error}<div class="error-banner" role="alert">{error}</div>{/if}
  {#if success}<div class="success-banner" role="status">{success}</div>{/if}
  {#if data.length === 0}<div class="empty-state">
      <KeyRound size={24} />
      <h3>No API keys configured</h3>
      <p>Add a key to restrict access to your proxy endpoints.</p>
    </div>
  {:else}
    <ul class="key-list">
      {#each data as key, index}
        <li class="key-row">
          <div class="key-summary">
            <span class="key-label">Key {index + 1}</span><code
              class="key-value"
              aria-label={revealed.includes(index)
                ? `API key ${index + 1}`
                : `API key ${index + 1}, hidden`}
              >{revealed.includes(index) ? key : '••••••••••••••••••••••••'}</code
            >
          </div>
          <div class="row-actions">
            <Button
              variant="ghost"
              size="icon"
              aria-label={`${revealed.includes(index) ? 'Hide' : 'Reveal'} key ${index + 1}`}
              aria-pressed={revealed.includes(index)}
              onclick={() =>
                (revealed = revealed.includes(index)
                  ? revealed.filter((item) => item !== index)
                  : [...revealed, index])}
              >{#if revealed.includes(index)}<EyeOff size={16} />{:else}<Eye
                  size={16}
                />{/if}</Button
            ><Button
              variant="ghost"
              size="icon"
              aria-label={`Copy key ${index + 1}`}
              onclick={() => copyKey(key)}><Copy size={15} /></Button
            ><Button
              variant="ghost"
              size="icon"
              disabled={disabled || busy || !canRemove(key)}
              aria-label={`Delete key ${index + 1}`}
              onclick={() => (deleting = key)}><Trash2 size={15} /></Button
            >
          </div>
          {#if deleting === key}<div class="delete-confirmation">
              <p>
                Delete key {index + 1}? Clients using it will lose access immediately.
              </p>
              <div class="row-actions">
                <Button variant="outline" disabled={busy} onclick={() => (deleting = null)}
                  >Cancel</Button
                ><Button
                  variant="destructive"
                  disabled={disabled || busy || !canRemove(key)}
                  onclick={() => deleteKey(key)}>{busy ? 'Deleting…' : 'Delete key'}</Button
                >
              </div>
            </div>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .add-key-form {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 14px;
    padding: 24px;
    border-bottom: 1px solid var(--border);
  }
  .key-field {
    flex: 1 1 220px;
    min-width: 0;
  }
  .key-field label {
    display: block;
    margin-bottom: 8px;
    font-weight: 500;
    font-size: 13px;
  }
  .key-field p {
    margin-top: 7px;
    font-size: 12px;
  }
  .key-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .key-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 18px;
    padding: 20px 24px;
    border-bottom: 1px solid var(--border);
  }
  .key-row:last-child {
    border-bottom: 0;
  }
  .key-summary {
    flex: 1 1 200px;
    min-width: 0;
  }
  .key-label {
    display: block;
    font-size: 12px;
    margin-bottom: 6px;
    color: var(--muted-foreground);
  }
  .key-value {
    display: block;
    font-size: 13px;
    overflow-wrap: anywhere;
    white-space: normal;
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
    .add-key-form,
    .key-row {
      padding: 16px;
    }
  }
</style>
