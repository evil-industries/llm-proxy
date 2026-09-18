<script lang="ts">
  import { BarChart } from 'layerchart';
  import { ChartNoAxesCombined } from '@lucide/svelte';
  import * as Chart from '$lib/components/ui/chart/index.js';
  import { aggregateRequestBuckets } from '$lib/chart';
  import type { AuthFile } from '$lib/api';

  let { files = [] }: { files?: AuthFile[] } = $props();
  const uid = $props.id();
  const config = {
    success: { label: 'Successful', color: 'var(--constructive)' },
    failed: { label: 'Failed', color: 'var(--destructive)' }
  } satisfies Chart.ChartConfig;
  const series = [
    { key: 'success', label: config.success.label, color: 'var(--color-success)' },
    { key: 'failed', label: config.failed.label, color: 'var(--color-failed)' }
  ];
  let buckets = $derived(aggregateRequestBuckets(files));
  let successful = $derived(buckets.reduce((sum, bucket) => sum + bucket.success, 0));
  let failed = $derived(buckets.reduce((sum, bucket) => sum + bucket.failed, 0));
  const number = (value: number) => new Intl.NumberFormat('en-US').format(value);
  const axisNumber = new Intl.NumberFormat('en-US', {
    notation: 'compact',
    maximumSignificantDigits: 3
  });
  let labels = $derived(new Map(buckets.map((bucket) => [bucket.id, bucket.time])));
</script>

<section class="panel request-chart" aria-labelledby={`request-chart-title-${uid}`}>
  <div class="panel-header">
    <div class="section-heading">
      <h2 id={`request-chart-title-${uid}`}>Request activity</h2>
      <p class="muted">Recent 10-minute intervals · server local time</p>
    </div>
    {#if buckets.length}<div
        class="chart-legend"
        aria-label="Request totals in displayed intervals"
      >
        <span
          ><i style:background={config.success.color}></i>Successful
          <strong>{number(successful)}</strong></span
        ><span
          ><i style:background={config.failed.color}></i>Failed
          <strong>{number(failed)}</strong></span
        >
      </div>{/if}
  </div>
  {#if buckets.length}
    <div class="plot" aria-hidden="true">
      <Chart.Container {config} class="h-64 min-h-64 w-full aspect-auto">
        <BarChart
          data={buckets}
          x="id"
          {series}
          seriesLayout="stack"
          bandPadding={0.35}
          axis
          grid={{ x: false, y: true }}
          padding={{ left: 0, right: 12, top: 12, bottom: 24 }}
          props={{
            xAxis: {
              format: (value: string) => labels.get(value)?.slice(0, 5) ?? '',
              tickSpacing: 80,
              tickOcclusion: true
            },
            yAxis: {
              ticks: 4,
              format: (value: number) => (Number.isInteger(value) ? axisNumber.format(value) : '')
            }
          }}
        >
          {#snippet tooltip()}<Chart.Tooltip
              labelFormatter={(value) => labels.get(String(value)) ?? ''}
            />{/snippet}
        </BarChart>
      </Chart.Container>
    </div>
    <details class="chart-data">
      <summary>View request data <span class="muted">({buckets.length} intervals)</span></summary>
      <table>
        <caption class="sr-only"
          >Recent request counts, grouped by the server's local 10-minute intervals.</caption
        ><thead
          ><tr
            ><th scope="col">Interval</th><th scope="col">Successful</th><th scope="col">Failed</th
            ></tr
          ></thead
        ><tbody
          >{#each buckets as bucket}<tr
              ><th scope="row">{bucket.time}</th><td>{number(bucket.success)}</td><td
                >{number(bucket.failed)}</td
              ></tr
            >{/each}</tbody
        >
      </table>
    </details>
  {:else}
    <div class="empty-state">
      <ChartNoAxesCombined size={25} />
      <h3>No request history yet</h3>
      <p>Activity appears when connected credentials report recent requests.</p>
    </div>
  {/if}
</section>

<style>
  .request-chart {
    min-width: 0;
  }
  .chart-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 10px 20px;
    font-size: 12px;
  }
  .chart-legend span {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 7px;
  }
  .chart-legend i {
    height: 8px;
    width: 8px;
    border-radius: 2px;
    flex-shrink: 0;
  }
  .chart-legend strong {
    font-weight: 500;
    font-variant-numeric: tabular-nums;
  }
  .plot {
    /* Axis labels render outside the plot. Scale their gutter with text size. */
    font-size: 12px;
    padding: 12px 20px 4px calc(4em + 8px);
    min-width: 0;
  }
  .chart-data {
    padding: 16px 24px;
    border-top: 1px solid var(--border);
    font-size: 12px;
  }
  summary {
    cursor: pointer;
    overflow-wrap: anywhere;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    margin-top: 16px;
    table-layout: fixed;
  }
  th,
  td {
    text-align: right;
    padding: 8px 4px;
    overflow-wrap: anywhere;
    border-bottom: 1px solid var(--border);
    font-variant-numeric: tabular-nums;
  }
  th {
    font-weight: 500;
  }
  th:first-child {
    text-align: left;
  }
  @media (max-width: 640px) {
    .plot {
      padding: 8px 8px 8px calc(4em + 8px);
    }
    .chart-data {
      padding: 16px;
    }
  }
</style>
