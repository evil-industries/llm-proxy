<script lang="ts">
  import { onMount } from 'svelte';
  import { Activity, ArrowUpRight, Timer, Zap } from '@lucide/svelte';
  import type { ManagementClient, ThroughputSnapshot } from '$lib/api';

  let { client, initialData }: { client: ManagementClient; initialData?: ThroughputSnapshot } =
    $props();
  let data = $state<ThroughputSnapshot>();
  let unavailable = $state(false);
  const snapshot = $derived(initialData ?? data);
  const metrics = $derived(snapshot?.metrics);
  const active = $derived(snapshot?.enabled && !unavailable);
  const number = (value: number | null | undefined, digits = 1) =>
    value == null || !active
      ? '—'
      : value.toLocaleString('en-US', { maximumFractionDigits: digits });
  const duration = (value: number | null | undefined) =>
    value == null || !active
      ? '—'
      : value >= 1000
        ? `${number(value / 1000, 2)} s`
        : `${number(value, 0)} ms`;

  onMount(() => {
    if (initialData) return;
    const controller = new AbortController();
    let pending = false;
    async function refresh() {
      if (pending || document.hidden || controller.signal.aborted) return;
      pending = true;
      try {
        const next = await client.getThroughput(controller.signal);
        if (!controller.signal.aborted) {
          data = next;
          unavailable = false;
        }
      } catch {
        if (!controller.signal.aborted) unavailable = true;
      } finally {
        pending = false;
      }
    }
    void refresh();
    const timer = setInterval(refresh, 5000);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      controller.abort();
      clearInterval(timer);
      document.removeEventListener('visibilitychange', refresh);
    };
  });
</script>

<section class="throughput" aria-label="Live throughput">
  <div class="throughput-heading">
    <h2><Activity size={14} />Live throughput</h2>
    <span class="muted"
      >All providers · rolling {metrics ? Math.round(metrics.window_seconds) : 300}s</span
    >
  </div>
  <div class="metrics">
    <div class="metric">
      <p><Zap size={13} />Output throughput</p>
      <strong>{number(metrics?.output_tokens_per_second)}<span>tok/s</span></strong>
      <span class="note">Output tokens per elapsed second</span>
    </div>
    <div class="metric">
      <p><ArrowUpRight size={13} />Request rate</p>
      <strong>{number(metrics?.requests_per_minute)}<span>req/min</span></strong>
      <span class="note">{number(metrics?.requests, 0)} completed upstream attempts</span>
    </div>
    <div class="metric">
      <p><Activity size={13} />Generation speed</p>
      <strong>{number(metrics?.generation_tokens_per_second)}<span>tok/s</span></strong>
      <span class="note">Streaming output ÷ generation time</span>
    </div>
    <div class="metric">
      <p><Timer size={13} />Time to first token</p>
      <strong>{duration(metrics?.average_ttft_ms)}</strong>
      <span class="note">Average reported first-token delay</span>
    </div>
  </div>
  <div class="throughput-footer">
    {#if unavailable}<span role="status">Throughput unavailable. Retrying automatically…</span>
    {:else if snapshot && !snapshot.enabled}<span
        >Enable usage statistics in Settings to collect throughput.</span
      >
    {:else if !snapshot}<span role="status">Waiting for runtime statistics…</span>
    {:else}<span
        >{number(metrics?.output_tokens, 0)} output tokens <span class="separator">·</span>
        {number(metrics?.failed, 0)} failed <span class="separator">·</span>
        {duration(metrics?.average_latency_ms)} average duration</span
      >{/if}
    <span>Completed attempts · refreshes every 5s</span>
  </div>
</section>

<style>
  .throughput {
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--background);
  }
  .throughput-heading {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: 8px;
    padding: 14px 18px 0;
  }
  h2 {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 12px;
  }
  .throughput-heading > span {
    font-size: 11px;
  }
  .metrics {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    padding: 16px 0;
  }
  .metric {
    min-width: 0;
    padding: 0 18px;
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 7px;
    overflow-wrap: anywhere;
  }
  .metric:last-child {
    border: 0;
  }
  .metric p {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--muted-foreground);
    font-size: 11px;
  }
  .metric p :global(svg) {
    flex-shrink: 0;
  }
  strong {
    font-size: 26px;
    font-weight: 600;
    line-height: 1.2;
    letter-spacing: -0.8px;
    font-variant-numeric: tabular-nums;
  }
  strong span {
    display: inline-block;
    font-size: 11px;
    letter-spacing: 0;
    font-weight: 400;
    margin-left: 5px;
    color: var(--muted-foreground);
  }
  .note {
    font-size: 10px;
    color: var(--muted-foreground);
    margin-top: auto;
  }
  .throughput-footer {
    border-top: 1px solid var(--border);
    background: var(--muted);
    padding: 8px 18px;
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 6px 16px;
    font-size: 10px;
    color: var(--muted-foreground);
    border-radius: 0 0 10px 10px;
    overflow-wrap: anywhere;
  }
  .separator {
    margin-inline: 5px;
  }
  @media (max-width: 1000px) {
    .metrics {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 20px 0;
    }
    .metric:nth-child(2) {
      border: 0;
    }
  }
  @media (max-width: 480px) {
    strong {
      font-size: 23px;
    }
    .metric {
      padding-inline: 12px;
    }
    .throughput-heading {
      padding-inline: 12px;
    }
    .throughput-footer {
      padding-inline: 12px;
    }
  }
</style>
