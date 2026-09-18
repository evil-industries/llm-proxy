<script lang="ts">
  import Switch from './switch.svelte';
  let { checked = false, disabled = false }: { checked?: boolean; disabled?: boolean } = $props();
</script>

<div style="padding:24px;display:flex;align-items:center;gap:12px">
  <Switch id="example-switch" bind:checked {disabled} /><label for="example-switch"
    >Enable credential</label
  >
</div>
