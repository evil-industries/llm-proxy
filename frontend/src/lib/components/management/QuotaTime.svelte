<script lang="ts">
  import { ClockCheck, TimerReset } from '@lucide/svelte';
  import { formatRelativeTime } from '$lib/datetime';
  import { resetLabel } from '$lib/quota';
  import * as Tooltip from '$lib/components/ui/tooltip/index.js';
  let { timestamp, now, kind }: { timestamp?: number; now: number; kind: 'reset' | 'observed' } =
    $props();
  const label = $derived(kind === 'reset' ? 'Reset' : 'Last checked');
  const Icon = $derived(kind === 'reset' ? TimerReset : ClockCheck);
  const valid = $derived(timestamp !== undefined && Number.isFinite(new Date(timestamp).getTime()));
</script>

{#if valid}
  <Tooltip.Provider>
    <Tooltip.Root>
      <Tooltip.Trigger>
        {#snippet child({ props })}
          <button {...props} aria-label={`${label}: ${formatRelativeTime(timestamp!, now)}`}>
            <time
              datetime={new Date(timestamp!).toISOString()}
              aria-label={`${label}: ${formatRelativeTime(timestamp!, now)}`}
            >
              <Icon size={13} aria-hidden="true" />{formatRelativeTime(timestamp!, now)}
            </time>
          </button>
        {/snippet}
      </Tooltip.Trigger>
      <Tooltip.Content sideOffset={6}>{resetLabel(timestamp)}</Tooltip.Content>
    </Tooltip.Root>
  </Tooltip.Provider>
{:else}
  <span><Icon size={13} aria-hidden="true" />{label} time unavailable</span>
{/if}

<style>
  button {
    display: inline-flex;
    padding: 0;
    border: 0;
    background: transparent;
    color: inherit;
    font: inherit;
    letter-spacing: inherit;
    cursor: help;
  }
  time,
  span {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    vertical-align: middle;
  }
  time :global(svg),
  span :global(svg) {
    flex-shrink: 0;
  }
</style>
