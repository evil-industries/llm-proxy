import type { PageServerLoad } from './$types';

export const load: PageServerLoad = ({ locals, url }) => ({
  oidc: Boolean(locals.managementConfiguration?.oidc),
  initialError: !locals.managementConfiguration
    ? 'Sign-in is not configured. Contact your administrator.'
    : url.searchParams.has('error')
      ? 'Authelia sign-in failed or access was denied. Try again or contact your administrator.'
      : ''
});
