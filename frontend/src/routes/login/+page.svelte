<script lang="ts">
  import LoginPanel from '$lib/components/LoginPanel.svelte';
</script>

<svelte:head><title>Sign in</title><meta name="robots" content="noindex,nofollow" /></svelte:head>
<main class="app-shell" aria-label="Sign in"><LoginPanel /></main>

<style>
  main {
    min-height: 100dvh;
    padding: 24px;
  }
</style>
