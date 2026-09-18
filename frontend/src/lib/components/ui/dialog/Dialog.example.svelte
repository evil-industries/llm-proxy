<script lang="ts">
  import * as Dialog from './index.js';
  import { buttonVariants } from '$lib/components/ui/button/index.js';
  let { open = false, longContent = false }: { open?: boolean; longContent?: boolean } = $props();
</script>

<div class="p-6">
  <Dialog.Root bind:open>
    <Dialog.Trigger class={buttonVariants({ variant: 'outline' })}>Inspect request</Dialog.Trigger>
    <Dialog.Content class="max-h-[calc(100dvh-2rem)] overflow-y-auto">
      <Dialog.Header
        ><Dialog.Title>Request details</Dialog.Title><Dialog.Description
          >Inspect the recorded response before closing this panel.</Dialog.Description
        ></Dialog.Header
      >
      <div class="min-w-0 space-y-3 break-words">
        <p>Status: completed</p>
        <p>Provider: codex</p>
        {#if longContent}<p>{'Long diagnostic message with useful context. '.repeat(40)}</p>{/if}
      </div>
      <Dialog.Footer
        ><Dialog.Close class={buttonVariants({ variant: 'outline' })}>Done</Dialog.Close
        ></Dialog.Footer
      >
    </Dialog.Content>
  </Dialog.Root>
</div>
