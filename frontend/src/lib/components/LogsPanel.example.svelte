<script lang="ts">
  import { createSessionManagementClient } from '$lib/api';
  import LogsPanel from './LogsPanel.svelte';
  let {
    empty = false,
    logging = true,
    failure = false,
    long = false,
    backlog = false
  }: {
    empty?: boolean;
    logging?: boolean;
    failure?: boolean;
    long?: boolean;
    backlog?: boolean;
  } = $props();
  let reads = 0;
  const lines = $derived([
    '[2026-09-17 09:04:11] [info] Server listening on :8317',
    '[2026-09-17 09:04:24] [info] POST /v1/responses status=200',
    '[2026-09-17 09:04:28] [warn] Credential temporarily unavailable; rotating provider',
    '[2026-09-17 09:04:29] [error] ' +
      'connection-retry-with-long-diagnostic-context-'.repeat(long ? 40 : 1)
  ]);
  const client = createSessionManagementClient(async (input) => {
    if (failure) return Response.json({}, { status: 502 });
    const url = new URL(String(input), 'http://story.invalid');
    if (url.pathname.endsWith('/request-logs'))
      return Response.json({
        files: empty
          ? []
          : [{ name: 'error-2026-demo-request.log', size: 84, modified: 1789635900, kind: 'error' }]
      });
    if (url.pathname.includes('/request-logs/'))
      return Response.json({
        name: 'error-2026-demo-request.log',
        text: 'POST /v1/responses\nStatus: 502\nProvider temporarily unavailable.',
        next_offset: 84,
        size: 84,
        modified: 1789635900,
        has_more: false
      });
    reads++;
    if (backlog && reads > 1 && reads <= 4) {
      await new Promise((resolve) => setTimeout(resolve, 800));
      const batch = Array.from(
        { length: 1000 },
        (_, i) => `[info] Backlog entry ${(reads - 2) * 1000 + i + 1}`
      );
      return Response.json({
        lines: batch,
        'line-count': batch.length,
        'latest-timestamp': 1789635900,
        'next-cursor': `story-cursor-${reads}`
      });
    }
    return Response.json({
      lines: empty || reads > 1 ? [] : lines,
      'line-count': empty ? 0 : lines.length,
      'latest-timestamp': 1789635900,
      'next-cursor': `story-cursor-${reads}`,
      'cursor-reset': false
    });
  });
</script>

<LogsPanel {client} config={{ 'logging-to-file': logging }} />
