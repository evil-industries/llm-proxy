<script lang="ts">
  import { cn, type WithElementRef } from '$lib/utils.js';
  import type { HTMLAttributes, HTMLTableAttributes } from 'svelte/elements';

  let {
    ref = $bindable(null),
    class: className,
    children,
    containerProps = {},
    ...restProps
  }: WithElementRef<HTMLTableAttributes> & {
    containerProps?: HTMLAttributes<HTMLDivElement>;
  } = $props();
</script>

<div
  {...containerProps}
  data-slot="table-container"
  class={cn('relative w-full overflow-x-auto', containerProps.class)}
>
  <table
    bind:this={ref}
    data-slot="table"
    class={cn('w-full caption-bottom text-sm', className)}
    {...restProps}
  >
    {@render children?.()}
  </table>
</div>
