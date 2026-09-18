<script lang="ts">
  import { createManagementClient, type AuthFile, type ManagementConfig } from '$lib/api';
  import CredentialsPanel from './CredentialsPanel.svelte';
  import KeysPanel from './KeysPanel.svelte';
  import SettingsPanel from './SettingsPanel.svelte';

  let {
    panel = 'credentials',
    empty = false,
    failure = false,
    longContent = false,
    disabled = false,
    finalKey = false,
    runtimeOnly = false
  }: {
    panel?: 'credentials' | 'keys' | 'settings';
    empty?: boolean;
    failure?: boolean;
    longContent?: boolean;
    disabled?: boolean;
    finalKey?: boolean;
    runtimeOnly?: boolean;
  } = $props();
  const standardFiles: AuthFile[] = [
    {
      name: 'claude-team.json',
      auth_index: 'story-claude',
      provider: 'claude',
      email: 'team@example.com',
      status: 'active'
    },
    {
      name: 'codex-development.json',
      auth_index: 'story-codex',
      provider: 'codex',
      email: 'development@example.com',
      disabled: true
    },
    {
      name: 'gemini-workspace.json',
      auth_index: 'story-gemini',
      provider: 'gemini',
      unavailable: true,
      status: 'error',
      status_message: 'Refresh the credential to restore access.'
    }
  ];
  let files = $state<AuthFile[]>([]);
  let keys = $state<string[]>([]);
  let config = $state<ManagementConfig>({});
  $effect(() => {
    files = empty
      ? []
      : runtimeOnly
        ? [
            {
              name: 'aistudio-connected',
              auth_index: 'story-runtime',
              provider: 'aistudio',
              runtime_only: true,
              disabled: false
            },
            {
              name: 'aistudio-paused',
              auth_index: 'story-runtime-disabled',
              provider: 'aistudio',
              runtime_only: true,
              disabled: true
            }
          ]
        : longContent
          ? [
              {
                ...standardFiles[0],
                name: `${'very-long-workspace-name-'.repeat(6)}.json`,
                email: `${'long-account-name-'.repeat(5)}@example.com`,
                status_message: 'The upstream provider is temporarily unavailable. '.repeat(8)
              }
            ]
          : structuredClone(standardFiles);
    keys = empty
      ? []
      : longContent
        ? [`sk-demo-${'long-key-for-overflow-testing-'.repeat(12)}`]
        : finalKey
          ? ['sk-demo-development-1234567890']
          : ['sk-demo-development-1234567890', 'sk-demo-production-0987654321'];
    config = {
      routing: { strategy: 'round-robin' },
      'logging-to-file': true,
      'usage-statistics-enabled': true,
      'ws-auth': true
    };
  });

  // Keep all story mutations in memory; the explorer never contacts a real instance.
  const client = createManagementClient({
    baseUrl: 'http://story.invalid',
    managementKey: 'garden-fixture-key',
    fetch: async (input, init) => {
      if (failure) return Response.json({}, { status: 500 });
      const url = new URL(String(input));
      const method = init?.method ?? 'GET';
      if (url.pathname.endsWith('/api-keys/mutate') && method === 'POST') {
        const { action, value } = JSON.parse(String(init?.body)) as {
          action: 'add' | 'remove';
          value: string;
        };
        if (action === 'add') {
          if (keys.includes(value)) return Response.json({}, { status: 409 });
          keys = [...keys, value];
        } else {
          if (!keys.includes(value) || new Set(keys).size <= 1)
            return Response.json({}, { status: 409 });
          keys = keys.filter((key) => key !== value);
        }
      } else if (url.pathname.endsWith('/api-keys') && method === 'GET') {
        return Response.json({ 'api-keys': keys });
      } else if (url.pathname.endsWith('/auth-files/status')) {
        const body = JSON.parse(String(init?.body)) as { name: string; disabled: boolean };
        files = files.map((file) =>
          file.name === body.name ? { ...file, disabled: body.disabled } : file
        );
      } else if (url.pathname.endsWith('/auth-files') && method === 'DELETE') {
        files = files.filter((file) => file.name !== url.searchParams.get('name'));
      } else if (url.pathname.endsWith('/auth-files') && method === 'POST') {
        const upload = (init?.body as FormData).get('file');
        if (upload instanceof File)
          files = [...files, { name: upload.name, provider: 'uploaded', status: 'active' }];
      } else if (method === 'PUT') {
        const body = JSON.parse(String(init?.body)) as {
          value: boolean | 'round-robin' | 'weighted-round-robin' | 'fill-first';
        };
        if (url.pathname.endsWith('/routing/strategy') && typeof body.value === 'string')
          config = { ...config, routing: { strategy: body.value } };
        else config = { ...config, [url.pathname.split('/').at(-1)!]: body.value };
      }
      return Response.json({});
    }
  });
  const onrefresh = async () => {};
</script>

<div style="padding:24px;min-width:0">
  {#if failure}<p class="muted" style="margin-bottom:16px">
      Try saving a change to inspect the server-error feedback. All requests are simulated.
    </p>{/if}
  {#if panel === 'credentials'}<CredentialsPanel {client} data={files} {onrefresh} {disabled} />
  {:else if panel === 'keys'}<KeysPanel {client} data={keys} {onrefresh} {disabled} />
  {:else}<SettingsPanel {client} data={config} {onrefresh} {disabled} />{/if}
</div>
