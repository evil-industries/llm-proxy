import { redirect } from '@sveltejs/kit';
import type { LayoutLoad } from './$types';

export const ssr = false;
export const prerender = false;

export const load: LayoutLoad = async ({ fetch, url }) => {
  const response = await fetch('/api/session', { credentials: 'same-origin' });
  const session = response.ok ? await response.json() : { configured: false };
  if (url.pathname !== '/login' && !session.authenticated) redirect(303, '/login');
  return { session };
};
