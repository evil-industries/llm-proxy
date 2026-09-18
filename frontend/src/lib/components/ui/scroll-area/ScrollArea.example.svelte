<script lang="ts">
  import * as ScrollArea from './index.js';
</script>

<div class="p-6">
  <ScrollArea.Root class="h-64 w-full rounded-lg border" type="always">
    <div class="space-y-3 p-4 text-sm">
      <h3 class="font-medium">Server activity</h3>
      {#each Array.from({ length: 30 }, (_, index) => index + 1) as index}<p>
          Request {index}: completed successfully.
        </p>{/each}
    </div>
  </ScrollArea.Root>
</div>
