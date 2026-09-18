<script lang="ts">
  import * as Card from './index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { RefreshCw } from '@lucide/svelte';
</script>

<div class="p-6">
  <Card.Root class="w-full max-w-lg">
    <Card.Header
      ><Card.Title>Connected account</Card.Title><Card.Description
        >Available for routing new requests.</Card.Description
      ><Card.Action
        ><Button variant="ghost" size="icon" aria-label="Refresh account"><RefreshCw /></Button
        ></Card.Action
      ></Card.Header
    >
    <Card.Content
      ><p>Requests: 128</p>
      <p class="mt-2 text-sm text-muted-foreground">
        Last response completed successfully.
      </p></Card.Content
    >
    <Card.Footer class="flex-wrap gap-2"
      ><Button variant="outline">Manage account</Button></Card.Footer
    >
  </Card.Root>
</div>
