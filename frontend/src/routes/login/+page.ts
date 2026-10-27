import type { PageLoad } from './$types';

export const load: PageLoad = async ({ parent, url }) => {
  const { session } = await parent();
  return {
    oidc: Boolean(session.oidc),
    initialError: !session.configured
      ? 'Sign-in is not configured. Contact your administrator.'
      : url.searchParams.has('error')
        ? 'Authelia sign-in failed or access was denied. Try again or contact your administrator.'
        : ''
  };
};
