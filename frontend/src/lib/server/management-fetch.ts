import { Agent, type Dispatcher } from 'undici';

// Scope this policy to management requests. Connection acquisition retains
// Undici's default timeout; established requests have no response deadlines.
// Override dispatch options too, so fetch cannot reinstate its own defaults.
const dispatcher = new Agent({ headersTimeout: 0, bodyTimeout: 0 }).compose(
  (dispatch) => (options, handler) =>
    dispatch({ ...options, headersTimeout: 0, bodyTimeout: 0 }, handler)
);

export const managementFetch: typeof fetch = (input, init) =>
  fetch(input, { ...init, dispatcher } as RequestInit & { dispatcher: Dispatcher });
