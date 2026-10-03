export default {
  name: 'Button',
  file: './button.example.svelte',
  examples: [
    { title: 'Primary', input: { label: 'Save changes' } },
    { title: 'Trust', input: { variant: 'trust', label: 'Approve access' } },
    { title: 'Constructive', input: { variant: 'constructive', label: 'Add key' } },
    { title: 'Warning', input: { variant: 'warning', label: 'Review quota' } },
    { title: 'Info', input: { variant: 'info', label: 'View details' } },
    { title: 'Outline', input: { variant: 'outline', label: 'Cancel' } },
    { title: 'Destructive', input: { variant: 'destructive', label: 'Delete credential' } },
    { title: 'Disabled', input: { disabled: true, label: 'Saving…' } }
  ]
};
