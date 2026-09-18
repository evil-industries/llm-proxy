export default {
  name: 'Sign in',
  file: './LoginPanel.svelte',
  examples: [
    { title: 'Default', input: {} },
    { title: 'Invalid password', input: { initialError: 'Incorrect password.' } },
    {
      title: 'Temporarily unavailable',
      input: { initialError: 'Sign-in is temporarily unavailable. Try again shortly.' }
    },
    {
      title: 'Not configured',
      input: { initialError: 'Sign-in is not configured. Contact your administrator.' }
    }
  ]
};
