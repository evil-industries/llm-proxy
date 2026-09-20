<script lang="ts">
  import AccountQuota from './AccountQuota.svelte';
  import UsagePanel from './UsagePanel.svelte';
  import type { AuthFile } from '$lib/api';
  let {
    single = false,
    missing = false,
    stale = false
  }: { single?: boolean; missing?: boolean; stale?: boolean } = $props();
  const now = Date.now();
  const files: AuthFile[] = $derived([
    {
      name: 'codex-team.json',
      email: 'team@example.com',
      provider: 'codex',
      quota: {
        observed_at: new Date(now - (stale ? 3600000 : 0)).toISOString(),
        signals: missing
          ? {}
          : {
              'x-codex-primary-used-percent': '35',
              'x-codex-primary-window-minutes': '300',
              'x-codex-primary-reset-at': String(Math.floor(now / 1000) + 7200),
              'x-codex-secondary-used-percent': '78',
              'x-codex-secondary-window-minutes': '10080',
              'x-codex-secondary-reset-at': String(Math.floor(now / 1000) + 172800)
            }
      }
    },
    {
      name: 'claude-work.json',
      email: 'work@example.com',
      provider: 'claude',
      quota: {
        observed_at: new Date(now).toISOString(),
        signals: {
          'anthropic-ratelimit-unified-5h-utilization': '0.94',
          'anthropic-ratelimit-unified-5h-reset': String(Math.floor(now / 1000) + 3600)
        }
      }
    },
    { name: 'other.json', provider: 'gemini', disabled: true }
  ]);
</script>

{#if single}<AccountQuota file={files[0]} />{:else}<UsagePanel {files} />{/if}
