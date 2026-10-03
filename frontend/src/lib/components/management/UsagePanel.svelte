<script lang="ts">
  import CodexSummary from './CodexSummary.svelte';
  import {
    isCodex,
    codexPlan,
    subscriptionState,
    subscriptionStateLabel,
    compareSubscriptions,
    subscriptionReset
  } from '$lib/codex';
  import { onMount } from 'svelte';
  import { Layers, Eye, EyeOff } from '@lucide/svelte';
  import type { AuthFile } from '$lib/api';
  import ProviderIcon from '$lib/components/ProviderIcon.svelte';
  import AccountQuota from './AccountQuota.svelte';
  import { accountQuota, quotaTone, quotaStale } from '$lib/quota';
  import QuotaTime from './QuotaTime.svelte';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  let { files, onaccounts }: { files: AuthFile[]; onaccounts?: () => void } = $props();
  let provider = $state<string>();
  let showEmails = $state(false);
  let now = $state(Date.now());
  onMount(() => {
    const timer = setInterval(() => (now = Date.now()), 30_000);
    return () => clearInterval(timer);
  });
  const providerOf = (file: AuthFile) =>
    (file.provider || file.type || 'Unknown').trim().toLowerCase();
  const reporting = $derived(files.filter((file) => accountQuota(file).windows.length > 0).length);
  function nextReset(file: AuthFile) {
    if (isCodex(file)) return subscriptionReset(file, now);
    if (file.disabled || file.unavailable) return Infinity;
    return Math.min(
      ...accountQuota(file)
        .windows.filter((w) => !quotaStale(w, now) && w.remaining > 0 && w.reset !== undefined)
        .map((w) => w.reset!)
    );
  }
  const groups = $derived(
    [...new Set(files.map(providerOf))]
      .sort((a, b) => Number(b === 'codex') - Number(a === 'codex') || a.localeCompare(b))
      .map((name) => {
        const accounts = files
          .filter((file) => providerOf(file) === name)
          .sort((a, b) =>
            name === 'codex'
              ? compareSubscriptions(a, b, now)
              : nextReset(a) - nextReset(b) || a.name.localeCompare(b.name)
          );
        const remaining = accounts
          .filter((file) => !file.disabled && !file.unavailable)
          .flatMap((file) => {
            const windows = accountQuota(file).windows.filter((w) => !quotaStale(w, now));
            return windows.length ? [Math.min(...windows.map((w) => w.remaining))] : [];
          });
        return {
          name,
          accounts,
          remaining: remaining.length
            ? remaining.reduce((sum, value) => sum + value, 0) / remaining.length
            : undefined,
          reset: Math.min(...accounts.map(nextReset)),
          reporting: remaining.length
        };
      })
  );
  const selected = $derived(
    provider === 'all' || groups.some((group) => group.name === provider)
      ? provider
      : groups.some((group) => group.name === 'codex')
        ? 'codex'
        : 'all'
  );
</script>

<section aria-labelledby="usage-title" class="quota-management">
  <div class="quota-toolbar">
    <div>
      <h2 id="usage-title">{selected === 'codex' ? 'Codex subscriptions' : 'Remaining usage'}</h2>
      <p class="muted mono">
        {files.length} credentials <span>·</span>
        {reporting} reporting usage
      </p>
    </div>
    <div class="quota-actions">
      {#if onaccounts}<Button variant="ghost" onclick={onaccounts}>Manage subscriptions</Button
        >{/if}
      <Button variant="ghost" onclick={() => (showEmails = !showEmails)}
        >{#if showEmails}<EyeOff size={14} />{:else}<Eye size={14} />{/if}{showEmails
          ? 'Hide emails'
          : 'Show emails'}</Button
      >
    </div>
  </div>
  {#if groups.length > 1}<div
      class="provider-tabs"
      role="group"
      aria-label="Filter usage by provider"
    >
      <button
        class:active={selected === 'all'}
        aria-pressed={selected === 'all'}
        onclick={() => (provider = 'all')}
        ><Layers size={16} />All <span>{files.length}</span></button
      >
      {#each groups as group}<button
          class:active={selected === group.name}
          aria-pressed={selected === group.name}
          onclick={() => (provider = group.name)}
          ><ProviderIcon provider={group.name} />{group.name}<span>{group.accounts.length}</span
          ></button
        >{/each}
    </div>{/if}
  {#if !files.length}<div class="panel empty-state">
      Connect an account to see its remaining usage.
    </div>
  {:else}
    {#if selected === 'codex'}<CodexSummary {files} {now} {showEmails} />{:else}
      <div class="provider-summaries">
        {#each groups.filter((group) => selected === 'all' || selected === group.name) as group}
          <div class="provider-summary" data-quota-tone={quotaTone(group.remaining)}>
            <div class="summary-heading">
              <h3>
                <ProviderIcon provider={group.name} />{group.name}
              </h3>
              <span class="muted">{group.accounts.length} credentials</span>
            </div>
            <p class="muted summary-label">Average remaining · tightest limit</p>
            <div class="summary-value">
              {group.remaining === undefined ? '—' : `${Math.round(group.remaining)}%`}<span
                class="muted"
              >
                available</span
              >
            </div>
            <div class="summary-bar" aria-hidden="true">
              <span style:width={`${group.remaining ?? 0}%`}></span>
            </div>
            <p class="muted reset-summary">
              {#if Number.isFinite(group.reset)}<QuotaTime
                  timestamp={group.reset}
                  {now}
                  kind="reset"
                />{:else}No upcoming reset reported{/if}
            </p>
            <p class="summary-note muted">
              {group.reporting} of {group.accounts.length} with fresh usage
            </p>
          </div>
        {/each}
      </div>{/if}
    {#each groups.filter((group) => selected === 'all' || selected === group.name) as group}
      <div class="provider-ledger">
        <div class="ledger-heading">
          <h3>{group.name}<span>{group.accounts.length}</span></h3>
          <span class="muted"
            >{group.name === 'codex'
              ? 'Available quota first · soonest reset'
              : 'Soonest reset first'}</span
          >
        </div>
        {#each group.accounts as file (file.auth_index || file.name)}
          {@const displayName = showEmails
            ? file.email || file.label || file.name
            : `${group.name} · ${files.indexOf(file) + 1}`}
          <article aria-label={`Usage for ${displayName}`}>
            <div class="account-heading">
              <h3 class="mono">
                {displayName}
              </h3>
              <p class="muted">
                {isCodex(file)
                  ? codexPlan(file) || 'Plan unavailable'
                  : file.account_type || providerOf(file)}
              </p>
              {#if isCodex(file)}
                {@const state = subscriptionState(file, now)}
                {#if state !== 'available' && state !== 'exhausted'}
                  <Badge variant={state === 'unavailable' ? 'destructive' : 'outline'}
                    >{subscriptionStateLabel[state]}</Badge
                  >
                {/if}
              {:else if file.disabled}<Badge variant="outline">Disabled</Badge
                >{:else if file.unavailable}<Badge variant="destructive">Unavailable</Badge>{/if}
            </div>
            <AccountQuota {file} {displayName} layout="ledger" />
          </article>
        {/each}
      </div>
    {/each}
  {/if}
</section>

<style>
  .quota-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .quota-management {
    min-width: 0;
  }
  .quota-toolbar,
  .summary-heading,
  .ledger-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    flex-wrap: wrap;
  }
  .quota-toolbar p {
    margin-top: 6px;
    font-size: 12px;
  }
  .quota-toolbar p span {
    margin: 0 8px;
  }
  .provider-tabs {
    display: flex;
    gap: 20px;
    flex-wrap: wrap;
    border-bottom: 1px solid var(--border);
    margin: 26px 0 22px;
  }
  .provider-tabs button {
    display: flex;
    align-items: center;
    gap: 8px;
    background: transparent;
    border: 0;
    border-bottom: 2px solid transparent;
    padding: 12px 4px;
    color: var(--muted-foreground);
    text-transform: capitalize;
    overflow-wrap: anywhere;
    min-width: 0;
    flex-wrap: wrap;
  }
  .provider-tabs button.active {
    border-bottom-color: var(--primary);
    color: var(--primary);
  }
  .provider-tabs button > span:last-child {
    font-size: 11px;
    background: var(--muted);
    padding: 1px 6px;
    border-radius: 5px;
  }
  .provider-summaries {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 230px), 1fr));
    gap: 24px 40px;
    padding-block: 8px 20px;
  }
  .provider-summary {
    padding-block: 8px;
    min-width: 0;
  }
  .summary-heading h3 {
    display: flex;
    align-items: center;
    gap: 9px;
    text-transform: capitalize;
  }
  .summary-heading > span,
  .summary-note,
  .reset-summary {
    font-size: 11px;
  }
  .summary-label {
    font-size: 12px;
    margin-top: 24px;
  }
  .summary-value {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 6px;
    font-size: 34px;
    letter-spacing: -1.3px;
    font-weight: 600;
    margin-top: 3px;
  }
  .summary-value > span {
    font-size: 12px;
    letter-spacing: 0;
    font-weight: 400;
  }
  .summary-bar {
    height: 5px;
    background: var(--muted);
    border-radius: 4px;
    margin: 13px 0 15px;
    overflow: hidden;
  }
  .summary-bar span {
    display: block;
    height: 100%;
    background: var(--quota-color);
    border-radius: inherit;
  }
  .reset-summary {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .summary-note {
    margin-top: 12px;
  }
  .provider-ledger {
    margin-top: 32px;
  }
  .ledger-heading {
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }
  .ledger-heading h3 {
    text-transform: capitalize;
  }
  .ledger-heading h3 span {
    margin-left: 10px;
    color: var(--muted-foreground);
    font-size: 12px;
    font-weight: 400;
  }
  .ledger-heading > span {
    font-size: 11px;
  }
  article {
    display: grid;
    grid-template-columns: minmax(150px, 0.8fr) minmax(0, 2fr);
    gap: 32px;
    padding: 24px 0;
    border-bottom: 1px solid var(--border);
  }
  .account-heading {
    min-width: 0;
  }
  .account-heading h3 {
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .account-heading p {
    font-size: 12px;
    margin: 7px 0;
    text-transform: capitalize;
  }
  @media (max-width: 1000px) {
    article {
      grid-template-columns: 1fr;
      gap: 16px;
    }
  }
  @media (max-width: 480px) {
    .provider-summary {
      padding: 12px 0;
    }
    .provider-tabs {
      gap: 12px;
    }
  }
</style>
