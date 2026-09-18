<script lang="ts">
  import Input from './input.svelte';
  let {
    disabled = false,
    invalid = false,
    value = '',
    placeholder = 'Enter a value'
  }: { disabled?: boolean; invalid?: boolean; value?: string; placeholder?: string } = $props();
</script>

<div style="padding:24px; max-width:440px">
  <label for="example-input" style="display:block;margin-bottom:8px">Server address</label><Input
    id="example-input"
    {disabled}
    aria-invalid={invalid}
    {value}
    {placeholder}
  />
</div>
