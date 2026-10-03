<script lang="ts">
  import type { AuthFile } from '$lib/api';
  import { subscriptionSummary } from '$lib/codex';
  import { quotaTone } from '$lib/quota';
  import QuotaTime from './QuotaTime.svelte';
  let {
    files,
    now,
    showEmails = false
  }: { files: AuthFile[]; now: number; showEmails?: boolean } = $props();
  const summary = $derived(subscriptionSummary(files, now));
  const metrics = $derived([
    ...(summary.hasShort ? [{ title: '5-hour remaining', value: summary.short }] : []),
    { title: 'Weekly remaining', value: summary.weekly }
  ]);
</script>

<div
  class="subscription-summary"
  style:--metric-columns={metrics.length + 2}
  role="group"
  aria-label="Codex subscription summary"
>
  <div class="metric">
    <span class="muted">Enabled subscriptions</span>
    <strong>{summary.enabled}<span class="muted"> / {summary.total}</span></strong>
    <span class="metric-note muted">{summary.exhausted} with exhausted quota</span>
  </div>
  {#each metrics as metric}
    <div class="metric" data-quota-tone={quotaTone(metric.value.remaining)}>
      <span class="muted">{metric.title}</span>
      <strong class="quota-value"
        >{metric.value.remaining === undefined
          ? '—'
          : `${Math.round(metric.value.remaining)}%`}</strong
      >
      <span class="metric-note muted"
        >Average · {metric.value.count} fresh {metric.value.count === 1
          ? 'account'
          : 'accounts'}</span
      >
    </div>
  {/each}
  <div class="metric">
    <span class="muted">Soonest usable reset</span>
    <strong class="reset-value"
      >{#if summary.next}<QuotaTime
          timestamp={summary.reset}
          {now}
          kind="reset"
        />{:else}—{/if}</strong
    >
    <span class="metric-note muted"
      >{summary.next
        ? showEmails
          ? summary.next.email || summary.next.name
          : `Codex · ${files.indexOf(summary.next) + 1}`
        : 'No fresh reset reported'}</span
    >
  </div>
</div>

<style>
  .subscription-summary {
    display: grid;
    grid-template-columns: repeat(var(--metric-columns), minmax(0, 1fr));
    gap: 24px 32px;
    padding-block: 24px 32px;
  }
  .metric {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  strong {
    font-size: 32px;
    font-weight: 600;
    line-height: 1.3;
    letter-spacing: -1px;
    font-variant-numeric: tabular-nums;
  }
  strong > span {
    font-size: 18px;
    font-weight: 400;
  }
  .quota-value {
    color: var(--quota-color);
  }
  .reset-value {
    font-size: 20px;
    letter-spacing: -0.4px;
  }
  .metric-note {
    font-size: 12px;
    margin-top: auto;
  }
  @media (max-width: 1200px) {
    .subscription-summary {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
  @media (max-width: 480px) {
    .subscription-summary {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
