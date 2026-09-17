export default {
  name: 'Settings panel',
  file: './Panel.example.svelte',
  description:
    'Interactive fixtures use an in-memory management client. No real credentials or network requests are used.',
  examples: [
    { title: 'Default', input: { panel: 'settings' } },
    { title: 'Mutation error', input: { panel: 'settings', failure: true } },
    { title: 'Read only', input: { panel: 'settings', disabled: true } }
  ]
};
