<script lang="ts">
  import * as Tabs from './index.js';
  let { orientation = 'horizontal' }: { orientation?: 'horizontal' | 'vertical' } = $props();
</script>

<div class="p-6">
  <Tabs.Root {orientation} value="server" class="w-full min-w-0">
    <Tabs.List aria-label="Log category" class="h-auto flex-wrap"
      ><Tabs.Trigger value="server">Server logs</Tabs.Trigger><Tabs.Trigger value="requests"
        >Requests</Tabs.Trigger
      ></Tabs.List
    >
    <Tabs.Content value="server" class="p-4"
      >Server started and is ready to accept requests.</Tabs.Content
    >
    <Tabs.Content value="requests" class="p-4"
      >The latest request completed successfully.</Tabs.Content
    >
  </Tabs.Root>
</div>
