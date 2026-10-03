export default {
  name: 'Provider icon',
  file: './ProviderIcon.svelte',
  description: 'Locally bundled monochrome provider marks. Pair with a visible provider name.',
  examples: [
    ...[
      'claude',
      'codex',
      'gemini',
      'antigravity',
      'vertex',
      'xai',
      'kimi',
      'meta',
      'devin',
      'openai',
      'qwen'
    ].map((provider) => ({ title: provider, input: { provider, size: 28 } })),
    { title: 'Unknown provider', input: { provider: 'custom', size: 28 } }
  ]
};
