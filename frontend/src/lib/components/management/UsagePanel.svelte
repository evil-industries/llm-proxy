<script lang="ts">
  import type { AuthFile } from '$lib/api';
  import AccountQuota from './AccountQuota.svelte';
  import { accountQuota } from '$lib/quota';
  import { Badge } from '$lib/components/ui/badge/index.js';
  let { files }: { files: AuthFile[] } = $props();
  const reporting = $derived(files.filter((file) => accountQuota(file).windows.length > 0).length);
</script>

<section class="panel" aria-labelledby="usage-title">
  <div class="panel-header">
    <div class="section-heading">
      <h2 id="usage-title">Remaining usage</h2>
      <p class="muted">
        {reporting} of {files.length} accounts reporting usage. Each limit resets independently.
      </p>
    </div>
  </div>
  {#if !files.length}<div class="empty-state">Connect an account to see its remaining usage.</div>
  {:else}<div class="usage-grid">
      {#each files as file (file.auth_index || file.name)}
        <article aria-label={`Usage for ${file.email || file.name}`}>
          <div class="account-heading">
            <div>
              <h3>{file.email || file.label || file.name}</h3>
              <p class="muted">
                {file.provider || file.type || 'Unknown provider'}{file.account_type
                  ? ` · ${file.account_type}`
                  : ''}
              </p>
            </div>
            {#if file.disabled}<Badge variant="outline">Disabled</Badge
              >{:else if file.unavailable}<Badge variant="destructive">Unavailable</Badge>{/if}
          </div>
          <AccountQuota {file} />
        </article>
      {/each}
    </div>{/if}
</section>

<style>
  .usage-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr));
    gap: 20px;
    padding: 24px;
  }
  article {
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 20px;
  }
  .account-heading {
    display: flex;
    justify-content: space-between;
    align-items: start;
    flex-wrap: wrap;
    gap: 10px;
    margin-bottom: 24px;
  }
  .account-heading > div {
    min-width: 0;
  }
  h3 {
    font-size: 14px;
    overflow-wrap: anywhere;
  }
  .account-heading p {
    font-size: 12px;
    margin-top: 5px;
  }
  @media (max-width: 400px) {
    .usage-grid {
      padding: 16px;
    }
    article {
      padding: 16px;
    }
  }
</style>
