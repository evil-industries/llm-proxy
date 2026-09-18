export default {
  name: 'Badge',
  file: './badge.example.svelte',
  examples: [
    { title: 'Active', input: { label: 'Active', variant: 'trust' } },
    { title: 'Success', input: { label: 'Saved', variant: 'constructive' } },
    { title: 'Error', input: { label: 'Error', variant: 'destructive' } },
    { title: 'Secondary', input: { label: 'Disabled', variant: 'secondary' } }
  ]
};
