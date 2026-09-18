<script lang="ts">
  import { getChartContext, Tooltip as TooltipPrimitive } from 'layerchart';
  import { cn, type WithElementRef, type WithoutChildren } from '$lib/utils.js';
  import { getPayloadConfigFromPayload, useChart, type TooltipPayload } from './chart-utils.js';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  function defaultFormatter(value: any, _payload: TooltipPayload[]) {
    return `${value}`;
  }

  let {
    ref = $bindable(null),
    class: className,
    hideLabel = false,
    indicator = 'dot',
    hideIndicator = false,
    labelKey,
    label,
    labelFormatter = defaultFormatter,
    labelClassName,
    formatter,
    nameKey,
    color,
    ...restProps
  }: WithoutChildren<WithElementRef<HTMLAttributes<HTMLDivElement>>> & {
    hideLabel?: boolean;
    label?: string;
    indicator?: 'line' | 'dot' | 'dashed';
    nameKey?: string;
    labelKey?: string;
    hideIndicator?: boolean;
    labelClassName?: string;
    labelFormatter?:
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ((value: any, payload: TooltipPayload[]) => string | number | Snippet) | null;
    formatter?: Snippet<
      [
        {
          value: unknown;
          name: string;
          item: TooltipPayload;
          index: number;
          payload: TooltipPayload[];
        }
      ]
    >;
  } = $props();

  const chart = useChart();
  const chartCtx = getChartContext();
  let geometry = $state({
    left: 0,
    top: 0,
    width: 0,
    height: 0,
    viewportWidth: 0,
    viewportHeight: 0
  });

  // Flipping sides cannot contain a tooltip wider than either side of the pointer.
  // Measure the portaled content and clamp its position to the viewport instead.
  $effect(() => {
    const content = ref;
    const container = chartCtx.containerRef;
    if (!content || !container) return;
    const measure = () => {
      const bounds = container.getBoundingClientRect();
      const tooltip = content.getBoundingClientRect();
      geometry = {
        left: bounds.left,
        top: bounds.top,
        width: tooltip.width,
        height: tooltip.height,
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight
      };
    };
    const observer = new ResizeObserver(measure);
    observer.observe(content);
    observer.observe(container);
    window.addEventListener('resize', measure);
    window.addEventListener('scroll', measure, true);
    measure();
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', measure);
      window.removeEventListener('scroll', measure, true);
    };
  });
  const clamp = (pointer: number, start: number, size: number, viewport: number) =>
    Math.max(8, Math.min(start + pointer + 10, viewport - size - 8)) - start;
  const tooltipX = $derived(
    clamp(chartCtx.tooltip.x, geometry.left, geometry.width, geometry.viewportWidth)
  );
  const tooltipY = $derived(
    clamp(chartCtx.tooltip.y, geometry.top, geometry.height, geometry.viewportHeight)
  );

  // Filter to series with defined values (important for item-based charts like Pie/Arc
  // where only the hovered item has a value)
  const visibleSeries = $derived(
    chartCtx.tooltip.series.filter((s: TooltipPayload) => s.value !== undefined)
  );

  const formattedLabel = $derived.by(() => {
    if (hideLabel || !visibleSeries?.length) return null;

    const [item] = visibleSeries;
    const tooltipData = chartCtx.tooltip.data;

    // Get the x-axis label value from the raw tooltip data (e.g. a Date or month string)
    const dataLabel = tooltipData != null ? chartCtx.x(tooltipData) : undefined;

    const key = labelKey ?? item?.label ?? item?.key ?? 'value';
    const itemConfig = getPayloadConfigFromPayload(
      chart.config,
      item,
      key,
      tooltipData as Record<string, unknown> | null
    );

    let value: unknown;
    if (!labelKey && typeof label === 'string') {
      value = chart.config[label as keyof typeof chart.config]?.label ?? label;
    } else if (labelKey) {
      value = itemConfig?.label ?? dataLabel;
    } else {
      value = dataLabel;
    }

    if (value === undefined) return null;
    if (!labelFormatter) return value;
    return labelFormatter(value, visibleSeries);
  });

  const nestLabel = $derived(visibleSeries.length === 1 && indicator !== 'dot');
</script>

{#snippet TooltipLabel()}
  {#if formattedLabel}
    <div class={cn('font-medium', labelClassName)}>
      {#if typeof formattedLabel === 'function'}
        {@render formattedLabel()}
      {:else}
        {formattedLabel}
      {/if}
    </div>
  {/if}
{/snippet}

<TooltipPrimitive.Root
  variant="none"
  x={tooltipX}
  y={tooltipY}
  xOffset={0}
  yOffset={0}
  motion="none"
  contained={false}
>
  <div
    bind:this={ref}
    class={cn(
      'grid min-w-32 max-w-[calc(100vw-1rem)] items-start gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl [overflow-wrap:anywhere]',
      className
    )}
    {...restProps}
  >
    {#if !nestLabel}
      {@render TooltipLabel()}
    {/if}
    <div class="grid gap-1.5">
      {#each visibleSeries as item, i (item.key + i)}
        {@const key = `${nameKey || item.key || item.label || 'value'}`}
        {@const itemConfig = getPayloadConfigFromPayload(
          chart.config,
          item,
          key,
          chartCtx.tooltip.data
        )}
        {@const indicatorColor = color || item.config?.color || item.color}
        <div
          class={cn(
            'flex w-full flex-wrap items-stretch gap-2 [&>svg]:h-2.5 [&>svg]:w-2.5 [&>svg]:text-muted-foreground',
            indicator === 'dot' && 'items-center'
          )}
        >
          {#if formatter && item.value !== undefined && item.label}
            {@render formatter({
              value: item.value,
              name: item.label,
              item,
              index: i,
              payload: visibleSeries
            })}
          {:else}
            {#if itemConfig?.icon}
              <itemConfig.icon />
            {:else if !hideIndicator}
              <div
                style="--color-bg: {indicatorColor}; --color-border: {indicatorColor};"
                class={cn('shrink-0 rounded-[2px] border-(--color-border) bg-(--color-bg)', {
                  'h-2.5 w-2.5': indicator === 'dot',
                  'h-full w-1': indicator === 'line',
                  'w-0 border-[1.5px] border-dashed bg-transparent': indicator === 'dashed',
                  'my-0.5': nestLabel && indicator === 'dashed'
                })}
              ></div>
            {/if}
            <div
              class={cn(
                'flex min-w-0 flex-1 flex-wrap justify-between gap-x-2 gap-y-1.5 leading-none',
                nestLabel ? 'items-end' : 'items-center'
              )}
            >
              <div class="grid gap-1.5">
                {#if nestLabel}
                  {@render TooltipLabel()}
                {/if}
                <span class="text-muted-foreground">
                  {itemConfig?.label || item.label}
                </span>
              </div>
              {#if item.value !== undefined}
                <span class="font-mono font-medium text-foreground tabular-nums">
                  {item.value.toLocaleString()}
                </span>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    </div>
  </div>
</TooltipPrimitive.Root>
