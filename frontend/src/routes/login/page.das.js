export default {
  name: 'Sign in route',
  file: './+page.svelte',
  examples: [
    { title: 'Password', input: { data: { oidc: false, initialError: '' } } },
    { title: 'Authelia', input: { data: { oidc: true, initialError: '' } } }
  ]
};
