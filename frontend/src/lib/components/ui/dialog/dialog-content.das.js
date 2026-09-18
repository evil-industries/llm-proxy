export default {
  name: 'Dialog Content',
  file: './Dialog.example.svelte',
  description:
    'Official shadcn-svelte dialog primitive shown in its complete interactive composition, including required parent context.',
  examples: [
    { title: 'Interactive', input: { open: false } },
    { title: 'Open', input: { open: true } },
    { title: 'Long content', input: { open: true, longContent: true } }
  ]
};
