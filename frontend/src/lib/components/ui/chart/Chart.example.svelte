<script lang="ts">
  import { BarChart } from 'layerchart';
  import * as Chart from './index.js';
  import ChartStyle from './chart-style.svelte';
  let { styleOnly = false }: { styleOnly?: boolean } = $props();
  const config = {
    successful: { label: 'Successful', color: '#2563eb' },
    failed: { label: 'Failed', color: '#dc2626' }
  } satisfies Chart.ChartConfig;
  const data = [
    { time: '09:00', successful: 20, failed: 2 },
    { time: '09:10', successful: 36, failed: 0 },
    { time: '09:20', successful: 28, failed: 3 }
  ];
</script>

<div style="padding:24px; min-width:0;">
  {#if styleOnly}
    <ChartStyle id="garden-style" {config} />
    <div data-chart="garden-style" style="display:grid;gap:12px">
      <h2>Chart style tokens</h2>
      <p class="muted">ChartStyle creates scoped color variables used by chart series.</p>
      <div style="height:24px;border-radius:4px;background:var(--color-successful)"></div>
      <div style="height:24px;border-radius:4px;background:var(--color-failed)"></div>
    </div>
  {:else}
    <h2>Request outcomes</h2>
    <p class="muted" style="margin:8px 0 16px">Hover a bar to inspect the tooltip.</p>
    <Chart.Container {config} class="h-64 w-full aspect-auto">
      <BarChart
        {data}
        x="time"
        series={[
          { key: 'successful', label: 'Successful', color: 'var(--color-successful)' },
          { key: 'failed', label: 'Failed', color: 'var(--color-failed)' }
        ]}
        seriesLayout="stack"
        axis="x"
      >
        {#snippet tooltip()}<Chart.Tooltip />{/snippet}
      </BarChart>
    </Chart.Container>
  {/if}
</div>
