export default {
  name: 'Input',
  file: './input.example.svelte',
  examples: [
    { title: 'Empty', input: { placeholder: 'https://proxy.example.com' } },
    { title: 'Populated', input: { value: 'https://proxy.example.com' } },
    { title: 'Invalid', input: { invalid: true, value: 'invalid server address' } },
    { title: 'Disabled', input: { disabled: true, value: 'https://proxy.example.com' } },
    {
      title: 'Long value',
      input: { value: 'https://proxy.example.com/' + 'long-path-segment-'.repeat(12) }
    }
  ]
};
