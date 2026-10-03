export default {
  name: 'Badge',
  file: './badge.example.svelte',
  examples: [
    { title: 'Active', input: { label: 'Active', variant: 'constructive' } },
    { title: 'Success', input: { label: 'Saved', variant: 'constructive' } },
    { title: 'Warning', input: { label: 'Awaiting approval', variant: 'warning' } },
    { title: 'Info', input: { label: 'Connecting', variant: 'info' } },
    { title: 'Error', input: { label: 'Error', variant: 'destructive' } },
    { title: 'Secondary', input: { label: 'Disabled', variant: 'secondary' } }
  ]
};
