<script lang="ts">
  import * as Alert from './index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { CircleAlert } from '@lucide/svelte';
  let { destructive = false }: { destructive?: boolean } = $props();
</script>

<div class="p-6">
  <Alert.Root variant={destructive ? 'destructive' : 'default'}>
    <CircleAlert />
    <Alert.Title
      >{destructive ? 'Request could not be completed' : 'Account authorization'}</Alert.Title
    >
    <Alert.Description
      >{destructive
        ? 'The provider is temporarily unavailable. Try again after reviewing the server logs.'
        : 'Complete authorization in your browser to connect the account.'}</Alert.Description
    >
    <Alert.Action><Button variant="ghost" size="xs">Review</Button></Alert.Action>
  </Alert.Root>
</div>
