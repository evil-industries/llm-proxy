const files = [
  { name: 'claude-team.json', provider: 'claude', success: 14280, failed: 38 },
  { name: 'codex-workspace.json', provider: 'codex', success: 8600, failed: 9 },
  { name: 'gemini-development.json', provider: 'gemini', disabled: true, success: 3250, failed: 21 }
];
export default {
  name: 'Overview',
  file: './Overview.svelte',
  examples: [
    { title: 'Connected instance', input: { files, keyCount: 3 } },
    { title: 'Empty instance', input: { files: [], keyCount: 0 } },
    {
      title: 'Long provider names',
      input: {
        files: [
          {
            name: 'workspace.json',
            provider: 'enterprise-provider-with-a-very-long-unbroken-name-for-layout-testing',
            success: 123456789,
            failed: 1000
          }
        ],
        keyCount: 1234
      }
    }
  ]
};
