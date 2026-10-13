export default {
  name: 'Log archive downloads',
  file: './LogArchivesPanel.example.svelte',
  examples: [
    { title: 'Local and S3 downloads' },
    { title: 'No archives yet', input: { empty: true } },
    { title: 'Archive storage unavailable', input: { failure: true } }
  ]
};
