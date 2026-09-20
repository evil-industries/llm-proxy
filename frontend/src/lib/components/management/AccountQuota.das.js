export default {
  name: 'Account quota',
  file: './Quota.example.svelte',
  examples: [
    { title: 'Usage windows', input: { single: true } },
    { title: 'Missing usage', input: { single: true, missing: true } },
    { title: 'Stale usage', input: { single: true, stale: true } }
  ]
};
