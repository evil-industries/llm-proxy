export default {
  name: 'Credentials panel',
  file: './Panel.example.svelte',
  description:
    'Interactive fixtures use an in-memory management client. No real credentials or network requests are used.',
  examples: [
    { title: 'Default', input: { panel: 'credentials' } },
    { title: 'Runtime credentials', input: { panel: 'credentials', runtimeOnly: true } },
    { title: 'Mutation error', input: { panel: 'credentials', failure: true } },
    { title: 'Read only', input: { panel: 'credentials', disabled: true } },
    { title: 'Empty', input: { panel: 'credentials', empty: true } },
    { title: 'Long content', input: { panel: 'credentials', longContent: true } }
  ]
};
