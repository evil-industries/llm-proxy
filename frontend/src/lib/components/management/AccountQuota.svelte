<script lang="ts">
  import { onMount } from 'svelte';
  import QuotaTime from './QuotaTime.svelte';
  import type { AuthFile } from '$lib/api';
  import { accountQuota, quotaTone, quotaExpired, quotaStale, QUOTA_MAX_AGE } from '$lib/quota';
  let {
    file,
    layout = 'stack',
    displayName
  }: { file: AuthFile; layout?: 'stack' | 'ledger'; displayName?: string } = $props();
  const quota = $derived(accountQuota(file));
  let now = $state(Date.now());
  onMount(() => {
    const timer = setInterval(() => (now = Date.now()), 30_000);
    return () => clearInterval(timer);
  });
  $effect(() => {
    const current = Math.max(now, Date.now());
    const deadlines = quota.windows.flatMap((window) => [
      window.observed + QUOTA_MAX_AGE,
      window.reset ?? 0
    ]);
    if (quota.observed) deadlines.push(quota.observed + QUOTA_MAX_AGE);
    const next = Math.min(...deadlines.filter((time) => time > current));
    if (!Number.isFinite(next)) return;
    // Also update precisely when a quota becomes stale or its reset passes.
    const timer = setTimeout(
      () => {
        now = Date.now();
      },
      Math.min(next - current + 1, 2147483647)
    );
    return () => clearTimeout(timer);
  });
</script>

<div
  class="account-quota"
  class:ledger={layout === 'ledger'}
  aria-label={`Remaining usage for ${displayName ?? (file.email || file.name)}`}
>
  {#if quota.windows.length}
    {#each quota.windows as window (window.key)}
      {@const expired = quotaExpired(window, now)}
      {@const stale = quotaStale(window, now)}
      <div
        class="quota-window"
        class:stale
        data-quota-tone={quotaTone(window.remaining, stale || file.disabled || file.unavailable)}
      >
        <div class="quota-heading">
          <span>{window.label}</span>
          <strong>
            {window.remaining.toLocaleString(undefined, { maximumFractionDigits: 1 })}% {stale
              ? 'last reported'
              : 'left'}
          </strong>
        </div>
        <progress max="100" value={window.remaining} aria-label={`${window.label} remaining`}
        ></progress>
        <div class="quota-meta">
          <QuotaTime timestamp={window.reset} {now} kind="reset" />
          <QuotaTime timestamp={window.observed} {now} kind="observed" />
        </div>
        {#if stale}<p class="quota-note">
            {expired ? 'Awaiting usage after reset.' : 'Usage may be out of date.'} Updates when the provider
            reports usage.
          </p>{/if}
      </div>
    {/each}
  {:else}
    <p class="quota-note">
      Remaining usage unavailable. This account has not reported a supported usage limit.
    </p>
  {/if}
  {#if quota.credits || quota.resetCredits !== undefined}
    <p class="quota-note">
      {quota.credits ?? ''}{quota.credits && quota.resetCredits !== undefined
        ? ' · '
        : ''}{quota.resetCredits !== undefined ? `${quota.resetCredits} banked resets` : ''}
      {#if quota.observed}
        · <QuotaTime
          timestamp={quota.observed}
          {now}
          kind="observed"
        />{#if now - quota.observed >= QUOTA_MAX_AGE}
          · May be out of date{/if}{/if}
    </p>
  {/if}
</div>

<style>
  .account-quota {
    display: grid;
    gap: 18px;
    min-width: 0;
    width: 100%;
  }
  .ledger {
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
    gap: 24px;
  }
  .ledger > .quota-note {
    grid-column: 1 / -1;
  }
  .quota-window {
    min-width: 0;
  }
  .quota-heading,
  .quota-meta {
    display: flex;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 4px 16px;
  }
  .quota-heading {
    font-size: 13px;
    align-items: baseline;
  }
  .quota-heading span {
    overflow-wrap: anywhere;
  }
  strong {
    color: var(--quota-color);
    font-variant-numeric: tabular-nums;
    font-size: 15px;
  }
  progress {
    appearance: none;
    display: block;
    border: 0;
    width: 100%;
    height: 7px;
    border-radius: 8px;
    overflow: hidden;
    background: var(--muted);
    margin: 8px 0;
  }
  progress::-webkit-progress-bar {
    background: var(--muted);
  }
  progress::-webkit-progress-value {
    background: var(--quota-color);
    border-radius: 8px;
  }
  progress::-moz-progress-bar {
    background: var(--quota-color);
    border-radius: 8px;
  }
  .stale progress {
    opacity: 0.4;
  }
  .quota-meta,
  .quota-note {
    font-size: 12px;
    color: var(--muted-foreground);
    line-height: 1.6;
  }
  .quota-note {
    margin: 0;
  }
</style>
