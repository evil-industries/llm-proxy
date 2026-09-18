export default {
  name: 'Connect Codex subscription',
  file: './CodexDeviceAuth.example.svelte',
  description:
    'Device-code setup and resumable approval states. These stories use local fixtures and never authenticate a real account.',
  examples: [
    { title: 'Connect an account', input: {} },
    { title: 'Awaiting approval', input: { status: 'pending' } },
    { title: 'Account connected', input: { status: 'complete' } },
    { title: 'Expired code', input: { status: 'expired' } },
    { title: 'Device authentication disabled', input: { status: 'error' } },
    { title: 'Cancelled', input: { status: 'cancelled' } },
    { title: 'Long device code', input: { status: 'pending', longContent: true } },
    { title: 'Long account identity', input: { status: 'complete', longContent: true } },
    { title: 'Read only', input: { disabled: true } }
  ]
};
