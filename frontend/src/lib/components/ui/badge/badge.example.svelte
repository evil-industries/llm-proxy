<script lang="ts">
  import Badge, { type BadgeVariant } from './badge.svelte';
  let { variant = 'outline', label = 'Active' }: { variant?: BadgeVariant; label?: string } =
    $props();
</script>

<div style="padding:24px"><Badge {variant}>{label}</Badge></div>
