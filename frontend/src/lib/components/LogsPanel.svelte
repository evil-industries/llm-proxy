<script lang="ts">
  import { Search, ScrollText } from '@lucide/svelte';
  import { Input } from '$lib/components/ui/input/index.js';
  let { lines = [] }: { lines?: string[] } = $props();
  let query = $state('');
  let filtered = $derived(lines.filter((line) => line.toLowerCase().includes(query.toLowerCase())));
</script>

<section class="panel">
  <div class="panel-header">
    <div class="section-heading">
      <h2>Server logs</h2>
      <p class="muted">Latest log lines from your instance.</p>
    </div>
    <div class="log-search">
      <Search size={15} /><Input
        aria-label="Search logs"
        placeholder="Search logs…"
        bind:value={query}
      />
    </div>
  </div>
  {#if filtered.length}
    <!-- Scrollable log output must be keyboard-focusable. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div class="log-lines" tabindex="0" role="region" aria-label="Log output">
      {#each filtered as line}<div class="log-line">{line}</div>{/each}
    </div>
  {:else}<div class="empty-state">
      <ScrollText size={26} />
      <h3>{query ? 'No matching log lines' : 'No logs available'}</h3>
      <p>
        {query
          ? 'Try a different search.'
          : 'Enable file logging in Settings to collect server logs.'}
      </p>
    </div>{/if}
  <div class="log-footer muted">
    {filtered.length} of {lines.length} lines · Refresh to fetch the latest
  </div>
</section>

<style>
  .log-search {
    display: flex;
    gap: 8px;
    align-items: center;
    width: 260px;
    max-width: 100%;
  }
  .log-search :global(svg) {
    flex-shrink: 0;
  }
  .log-lines {
    padding: 12px 0;
    max-height: 560px;
    overflow: auto;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
    line-height: 1.8;
  }
  .log-line {
    padding: 6px 24px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    border-bottom: 1px solid #f5f5f5;
  }
  .log-footer {
    padding: 14px 24px;
    border-top: 1px solid #ededed;
    font-size: 12px;
  }
</style>
