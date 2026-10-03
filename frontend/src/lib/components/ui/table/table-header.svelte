<script lang="ts">
  import { cn, type WithElementRef } from '$lib/utils.js';
  import type { HTMLAttributes } from 'svelte/elements';

  let {
    ref = $bindable(null),
    class: className,
    children,
    sticky = false,
    ...restProps
  }: WithElementRef<HTMLAttributes<HTMLTableSectionElement>> & { sticky?: boolean } = $props();
</script>

<thead
  bind:this={ref}
  data-slot="table-header"
  class={cn('[&_tr]:border-b', sticky && 'sticky top-0 z-10 bg-background', className)}
  {...restProps}
>
  {@render children?.()}
</thead>
