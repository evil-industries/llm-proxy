export default {
  name: 'Keys panel',
  file: './Panel.example.svelte',
  description:
    'Interactive fixtures use an in-memory management client. No real credentials or network requests are used.',
  examples: [
    { title: 'Default', input: { panel: 'keys' } },
    { title: 'Last key protected', input: { panel: 'keys', finalKey: true } },
    { title: 'Mutation error', input: { panel: 'keys', failure: true } },
    { title: 'Read only', input: { panel: 'keys', disabled: true } },
    { title: 'Empty', input: { panel: 'keys', empty: true } },
    { title: 'Long content', input: { panel: 'keys', longContent: true } }
  ]
};
