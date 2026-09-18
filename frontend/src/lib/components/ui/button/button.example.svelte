<script lang="ts">
  import Button, { type ButtonVariant } from './button.svelte';
  let {
    variant = 'default',
    disabled = false,
    label = 'Save changes'
  }: { variant?: ButtonVariant; disabled?: boolean; label?: string } = $props();
</script>

<div style="padding:24px; display:flex; flex-wrap:wrap; gap:12px">
  <Button {variant} {disabled}>{label}</Button>
</div>
