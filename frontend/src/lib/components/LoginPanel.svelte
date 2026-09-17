<script lang="ts">
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  let {
    onauthenticated = () => window.location.assign('/'),
    initialError = ''
  }: {
    onauthenticated?: () => void;
    initialError?: string;
  } = $props();
  let password = $state('');
  let busy = $state(false);
  let error = $state('');
  async function login(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    error = '';
    try {
      const response = await fetch('/api/session', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ password })
      });
      if (!response.ok) {
        if (response.status === 503) {
          const body: unknown = await response.json().catch(() => null);
          const notConfigured =
            body !== null &&
            typeof body === 'object' &&
            'code' in body &&
            body.code === 'not_configured';
          error = notConfigured
            ? 'Sign-in is not configured. Contact your administrator.'
            : 'Sign-in is temporarily unavailable. Try again shortly.';
          return;
        }
        error =
          response.status === 401
            ? 'Incorrect password.'
            : response.status === 429
              ? 'Too many sign-in attempts. Try again later.'
              : 'Unable to sign in. Please try again.';
        return;
      }
      password = '';
      onauthenticated();
    } catch {
      error = 'Unable to reach the server. Please try again.';
    } finally {
      busy = false;
    }
  }
</script>

<div class="login-panel">
  <div class="section-heading">
    <h1>Sign in</h1>
    <p class="muted">Enter your password to access management.</p>
  </div>
  <form class="panel login-form" onsubmit={login}>
    <div class="field">
      <label for="password">Password</label><Input
        id="password"
        name="password"
        type="password"
        autocomplete="current-password"
        bind:value={password}
        required
        maxlength={1024}
        disabled={busy}
      />
    </div>
    {#if error || initialError}<p class="error-banner" role="alert">{error || initialError}</p>{/if}
    <Button type="submit" disabled={busy || !password}>{busy ? 'Signing in…' : 'Sign in'}</Button>
  </form>
</div>

<style>
  .login-panel {
    width: 100%;
    max-width: 400px;
    margin: 12vh auto;
    display: grid;
    gap: 26px;
  }
  .login-form {
    padding: 26px;
    display: grid;
    gap: 24px;
  }
  .section-heading {
    gap: 10px;
  }
</style>
