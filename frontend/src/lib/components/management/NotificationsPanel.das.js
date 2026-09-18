export default {
  name: 'Quota notifications',
  file: './NotificationsPanel.example.svelte',
  description:
    'Configure quota alerts, quota resets, and Codex banked reset notifications. All saves and tests are simulated in memory.',
  examples: [
    { title: 'Not configured', input: {} },
    { title: 'Saved destination and token', input: { configured: true } },
    { title: 'Only low quota alerts', input: { configured: true, resetEvents: false } },
    { title: 'Delivery failure', input: { configured: true, failure: true } },
    { title: 'Read only', input: { configured: true, disabled: true } }
  ]
};
