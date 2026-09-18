<script lang="ts">
  import { untrack } from 'svelte';
  import type { ManagementClient, CodexDeviceAuth as DeviceAuth } from '$lib/api';
  import CodexDeviceAuth from './CodexDeviceAuth.svelte';
  let {
    status = 'idle',
    disabled = false,
    longContent = false
  }: { status?: DeviceAuth['status']; disabled?: boolean; longContent?: boolean } = $props();
  function pending(): DeviceAuth {
    return {
      status: 'pending',
      user_code: longContent ? 'LONG-DEVICE-CODE-'.repeat(6) : 'ABCD-EFGH',
      verification_uri: 'https://auth.openai.com/codex/device',
      expires_at: new Date(Date.now() + 600_000).toISOString(),
      interval: 3600
    };
  }
  let saved: DeviceAuth = untrack(() =>
    status === 'pending'
      ? pending()
      : {
          status,
          ...(status === 'complete'
            ? {
                account: {
                  name: 'Operator',
                  email: longContent
                    ? `${'long.account.'.repeat(12)}@example.com`
                    : 'operator@example.com',
                  plan_type: 'Plus'
                }
              }
            : {}),
          ...(status === 'error'
            ? {
                error:
                  'Device authentication is disabled. Enable it in your ChatGPT security settings, then try again.'
              }
            : {})
        }
  );
  const client = {
    getCodexDeviceAuth: async () => structuredClone(saved),
    startCodexDeviceAuth: async () => structuredClone((saved = pending())),
    cancelCodexDeviceAuth: async () => structuredClone((saved = { status: 'cancelled' }))
  } as ManagementClient;
</script>

<div style="max-width: 52rem; padding: 1rem; margin-inline: auto;">
  <CodexDeviceAuth {client} {disabled} />
</div>
