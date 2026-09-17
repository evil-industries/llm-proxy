export default {
  name: 'Server logs',
  file: './LogsPanel.svelte',
  examples: [
    {
      title: 'Recent activity',
      input: {
        lines: [
          '2026-09-17 09:04:11 INFO  Server listening on :8317',
          '2026-09-17 09:04:24 INFO  POST /v1/responses status=200 provider=codex',
          '2026-09-17 09:04:28 WARN  Credential temporarily unavailable; rotating provider'
        ]
      }
    },
    { title: 'Empty log', input: { lines: [] } },
    {
      title: 'Long lines and errors',
      input: {
        lines: [
          '2026-09-17 ERROR Provider unavailable: ' +
            'connection-retry-with-long-diagnostic-context-'.repeat(14),
          ...Array.from(
            { length: 40 },
            (_, index) => `2026-09-17 INFO Request ${index + 1} completed`
          )
        ]
      }
    }
  ]
};
