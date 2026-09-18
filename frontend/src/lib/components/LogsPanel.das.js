export default {
  name: 'Log inspection',
  file: './LogsPanel.example.svelte',
  examples: [
    { title: 'Live application and saved request logs' },
    { title: 'Catch up with a request burst', input: { backlog: true } },
    { title: 'Empty logs', input: { empty: true } },
    { title: 'Long diagnostic lines', input: { long: true } },
    { title: 'File logging disabled', input: { logging: false } },
    { title: 'Server unavailable', input: { failure: true } }
  ]
};
