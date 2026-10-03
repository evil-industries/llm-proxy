<script lang="ts" module>
  import { type VariantProps, tv } from 'tailwind-variants';

  export const alertVariants = tv({
    base: "grid gap-0.5 rounded-lg border px-2.5 py-2 text-left text-sm has-data-[slot=alert-action]:relative has-data-[slot=alert-action]:pr-18 has-[>svg]:grid-cols-[auto_1fr] has-[>svg]:gap-x-2 *:[svg]:row-span-2 *:[svg]:translate-y-0.5 *:[svg]:text-current *:[svg:not([class*='size-'])]:size-4 group/alert relative w-full",
    variants: {
      variant: {
        default: 'bg-card text-card-foreground',
        constructive:
          'border-constructive-border bg-constructive-subtle text-constructive *:data-[slot=alert-description]:text-constructive',
        destructive:
          'border-destructive-border bg-destructive-subtle text-destructive *:data-[slot=alert-description]:text-destructive',
        warning:
          'border-warning-border bg-warning-subtle text-warning *:data-[slot=alert-description]:text-warning',
        info: 'border-info-border bg-info-subtle text-info *:data-[slot=alert-description]:text-info'
      }
    },
    defaultVariants: {
      variant: 'default'
    }
  });

  export type AlertVariant = VariantProps<typeof alertVariants>['variant'];
</script>

<script lang="ts">
  import { cn, type WithElementRef } from '$lib/utils.js';
  import type { HTMLAttributes } from 'svelte/elements';

  let {
    ref = $bindable(null),
    class: className,
    variant = 'default',
    children,
    ...restProps
  }: WithElementRef<HTMLAttributes<HTMLDivElement>> & {
    variant?: AlertVariant;
  } = $props();
</script>

<div
  bind:this={ref}
  data-slot="alert"
  role="alert"
  class={cn(alertVariants({ variant }), className)}
  {...restProps}
>
  {@render children?.()}
</div>
