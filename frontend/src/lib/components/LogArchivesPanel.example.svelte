<script lang="ts">
  import { createSessionManagementClient } from '$lib/api';
  import LogArchivesPanel from './LogArchivesPanel.svelte';
  let { empty = false, failure = false }: { empty?: boolean; failure?: boolean } = $props();
  const client = createSessionManagementClient(async (input) => {
    if (failure) return Response.json({}, { status: 502 });
    const url = new URL(String(input), 'http://story.invalid');
    const more = url.searchParams.has('cursor');
    return Response.json({
      files: empty
        ? []
        : more
          ? [
              {
                source: 's3',
                key: 'logs/production/error-responses-2026-10-13-req-def.log.gz',
                name: 'error-responses-2026-10-13-req-def.log.gz',
                size: 821,
                modified: 1791884100,
                kind: 'error'
              }
            ]
          : [
              {
                source: 'local',
                key: 'main.log',
                name: 'main.log',
                size: 45128,
                modified: 1791885900,
                kind: 'application'
              },
              {
                source: 's3',
                key: 'logs/production/main-2026-10-13T090000.log.gz',
                name: 'main-2026-10-13T090000.log.gz',
                size: 2359296,
                modified: 1791885600,
                kind: 'application'
              },
              {
                source: 's3',
                key: 'logs/production/request-responses-2026-10-13-req-abc.log.gz',
                name: 'request-responses-2026-10-13-req-abc.log.gz',
                size: 18432,
                modified: 1791885000,
                kind: 'request'
              }
            ],
      next_cursor: empty || more ? '' : 'next-page'
    });
  });
</script>

<div class="panel flex flex-col min-h-[400px]"><LogArchivesPanel {client} /></div>
