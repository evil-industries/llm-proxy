<script lang="ts">
  import * as Select from './index.js';
  let value = $state('all');
  const items = [
    { value: 'all', label: 'All providers' },
    { value: 'codex', label: 'Codex' },
    { value: 'claude', label: 'Claude' },
    ...Array.from({ length: 24 }, (_, index) => ({
      value: `workspace-${index + 1}`,
      label: `Workspace ${index + 1}`
    }))
  ];
  let { disabled = false }: { disabled?: boolean } = $props();
</script>

<div class="p-6">
  <Select.Root type="single" bind:value {disabled} {items}>
    <Select.Trigger aria-label="Filter by provider" class="w-full max-w-72"
      ><Select.Value placeholder="Select provider" /></Select.Trigger
    >
    <Select.Content>
      <Select.Group
        ><Select.GroupHeading>Available providers</Select.GroupHeading><Select.Item
          value="all"
          label="All providers">All providers</Select.Item
        ><Select.Item value="codex" label="Codex">Codex</Select.Item><Select.Item
          value="claude"
          label="Claude">Claude</Select.Item
        ></Select.Group
      >
      <Select.Separator />
      <Select.Label>Additional workspaces</Select.Label>
      {#each Array.from({ length: 24 }, (_, index) => index + 1) as index}<Select.Item
          value={`workspace-${index}`}
          label={`Workspace ${index}`}>Workspace {index}</Select.Item
        >{/each}
    </Select.Content>
  </Select.Root>
  <p class="mt-3 text-sm text-muted-foreground">Selected: {value}</p>
</div>
