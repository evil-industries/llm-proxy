<script lang="ts">
  import { Network } from '@lucide/svelte';
  import claude from '$lib/assets/providers/claude.svg?url';
  import codex from '$lib/assets/providers/codex.svg?url';
  import gemini from '$lib/assets/providers/gemini.svg?url';
  import antigravity from '$lib/assets/providers/antigravity.svg?url';
  import vertex from '$lib/assets/providers/vertexai.svg?url';
  import xai from '$lib/assets/providers/xai.svg?url';
  import kimi from '$lib/assets/providers/kimi.svg?url';
  import meta from '$lib/assets/providers/meta.svg?url';
  import devin from '$lib/assets/providers/devin.svg?url';
  import openai from '$lib/assets/providers/openai.svg?url';
  import qwen from '$lib/assets/providers/qwen.svg?url';

  let { provider, size = 20 }: { provider: string; size?: number } = $props();
  const marks: Record<string, string> = {
    claude,
    anthropic: claude,
    'claude-code': claude,
    codex,
    openai,
    gemini,
    'gemini-cli': gemini,
    google: gemini,
    antigravity,
    vertex,
    'vertex-ai': vertex,
    vertexai: vertex,
    xai,
    grok: xai,
    kimi,
    moonshot: kimi,
    meta,
    devin,
    qwen
  };
  const source = $derived(marks[provider.trim().toLowerCase()]);
</script>

<span class="provider-mark" aria-hidden="true" style:width={`${size}px`} style:height={`${size}px`}>
  {#if source}
    <span class="logo" style:mask-image={`url("${source}")`}></span>
  {:else}
    <Network {size} strokeWidth={1.5} />
  {/if}
</span>

<style>
  .provider-mark {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    vertical-align: middle;
  }
  .logo {
    width: 100%;
    height: 100%;
    background: currentColor;
    mask-size: contain;
    mask-repeat: no-repeat;
    mask-position: center;
  }
</style>
