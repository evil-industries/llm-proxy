<script lang="ts">
  import { onMount } from 'svelte';
  import { Check, Copy, ExternalLink, KeyRound, LoaderCircle } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import * as Card from '$lib/components/ui/card/index.js';
  import * as Alert from '$lib/components/ui/alert/index.js';
  import type { ManagementClient, CodexDeviceAuth as DeviceAuth } from '$lib/api';

  let {
    client,
    disabled = false,
    onconnected = async () => {}
  }: {
    client: ManagementClient;
    disabled?: boolean;
    onconnected?: () => Promise<void>;
  } = $props();

  let flow = $state<DeviceAuth | null>(null);
  let busy = $state<'load' | 'start' | 'cancel' | null>(null);
  let notice = $state('');
  let refreshFailed = $state(false);
  let copied = $state(false);
  let copyFailed = $state(false);
  let uncertain = $state(false);
  let now = $state(Date.now());
  let disposed = false;
  let controller: AbortController | undefined;
  let poll: ReturnType<typeof setTimeout> | undefined;
  const pending = $derived(flow?.status === 'pending');
  const complete = $derived(flow?.status === 'complete');
  const expiry = $derived(flow?.expires_at ? Date.parse(flow.expires_at) : NaN);
  const remaining = $derived(
    Number.isFinite(expiry) ? Math.max(0, Math.ceil((expiry - now) / 1000)) : null
  );
  const countdown = $derived(
    remaining === null
      ? ''
      : `${Math.floor(remaining / 60)}:${String(remaining % 60).padStart(2, '0')}`
  );

  function schedule() {
    clearTimeout(poll);
    if (!disposed && (pending || uncertain)) {
      poll = setTimeout(() => void perform('load'), Math.max(1, flow?.interval ?? 5) * 1000);
    }
  }

  async function refreshAccounts() {
    refreshFailed = false;
    try {
      await onconnected();
    } catch {
      if (!disposed) refreshFailed = true;
    }
  }

  async function perform(operation: 'load' | 'start' | 'cancel') {
    if (busy || disposed) return;
    clearTimeout(poll);
    busy = operation;
    notice = '';
    controller = new AbortController();
    try {
      const next = await (operation === 'start'
        ? client.startCodexDeviceAuth(controller.signal)
        : operation === 'cancel'
          ? client.cancelCodexDeviceAuth(controller.signal)
          : client.getCodexDeviceAuth(controller.signal));
      if (disposed) return;
      const newlyConnected = next.status === 'complete' && flow?.status !== 'complete';
      if (next.user_code !== flow?.user_code) {
        copied = false;
        copyFailed = false;
      }
      flow = next;
      uncertain = false;
      now = Date.now();
      // Cancellation can lose a race to successful approval. Trust the server's
      // final flow so a saved account is never presented as cancelled.
      if (newlyConnected) await refreshAccounts();
    } catch {
      if (!disposed) {
        uncertain = true;
        notice =
          operation === 'cancel'
            ? 'Cancellation could not be confirmed. Checking the connection again.'
            : operation === 'start'
              ? 'The connection request could not be confirmed. Checking its status before trying again.'
              : pending
                ? 'Unable to check the connection. Your approval request has been kept. Retrying automatically.'
                : 'Unable to check connection status. Retrying automatically; you can also check again below.';
      }
    } finally {
      if (!disposed) {
        busy = null;
        schedule();
      }
    }
  }

  async function copyCode() {
    copied = false;
    copyFailed = false;
    try {
      await navigator.clipboard.writeText(flow?.user_code ?? '');
      copied = true;
    } catch {
      copyFailed = true;
    }
  }

  onMount(() => {
    void perform('load');
    const clock = setInterval(() => {
      now = Date.now();
    }, 1000);
    return () => {
      disposed = true;
      clearTimeout(poll);
      clearInterval(clock);
      controller?.abort();
      // Leaving the page only stops this view; the server retains the flow.
    };
  });
</script>

<Card.Root class="device-auth min-w-0" role="region" aria-label="Connect a Codex account">
  <Card.Header>
    <div class="device-heading">
      <div class="device-symbol"><KeyRound size={20} aria-hidden="true" /></div>
      <div class="min-w-0">
        <Card.Title role="heading" aria-level={2}>Connect your Codex subscription</Card.Title>
        <Card.Description
          >Approve access with your ChatGPT account. Your credentials are saved securely on the
          gateway.</Card.Description
        >
      </div>
      {#if complete}<Badge variant="secondary"><Check aria-hidden="true" />Connected</Badge
        >{:else if pending}<Badge variant="outline">Awaiting approval</Badge>{/if}
    </div>
  </Card.Header>
  <Card.Content class="device-content">
    {#if notice}
      <Alert.Root role="alert"
        ><Alert.Title>Connection status unavailable</Alert.Title><Alert.Description
          >{notice}</Alert.Description
        ></Alert.Root
      >
    {/if}
    {#if !flow}
      <p role="status" class="device-muted">
        {busy
          ? 'Checking for an existing connection request…'
          : 'Connection status could not be loaded.'}
      </p>
    {:else if complete}
      <div role="status" class="device-success">
        <Check size={22} aria-hidden="true" />
        <div>
          <h3>Codex account connected</h3>
          <p>
            {flow.account?.email || flow.account?.name || 'Your subscription is ready to use.'}
          </p>
          {#if flow.account?.plan_type}<p class="device-muted">
              Plan: {flow.account.plan_type}
            </p>{/if}
        </div>
      </div>
      {#if refreshFailed}<Alert.Root role="alert"
          ><Alert.Description
            >The account is connected, but the credential list could not refresh.</Alert.Description
          ></Alert.Root
        >{/if}
    {:else if pending}
      <ol class="device-steps">
        <li>
          <span class="device-step" aria-hidden="true">1</span>
          <div>
            <h3>Copy your one-time code</h3>
            <p class="device-muted">
              Only enter this code on the approval page you open below. Do not share it.
            </p>
            <div class="device-code">
              <output aria-label="One-time device code"><code>{flow.user_code}</code></output
              ><Button
                variant="outline"
                onclick={copyCode}
                disabled={remaining === 0 || !flow.user_code}
                ><Copy aria-hidden="true" />{copied ? 'Copied' : 'Copy code'}</Button
              >
            </div>
            {#if copyFailed}<p role="status" class="device-muted">
                Copy is unavailable. Select the code and copy it manually.
              </p>{/if}
            <p role="timer" aria-live="off" aria-label="Code expiration" class="device-muted">
              {remaining === 0
                ? 'The code has expired. Checking the final connection status…'
                : remaining !== null
                  ? `Code expires in ${countdown}`
                  : 'The code is valid for a limited time.'}
            </p>
          </div>
        </li>
        <li>
          <span class="device-step" aria-hidden="true">2</span>
          <div>
            <h3>Approve access in your browser</h3>
            <p class="device-muted">
              Sign in with the ChatGPT account whose Codex subscription you want to connect.
            </p>
            {#if remaining !== 0}<Button
                href="https://auth.openai.com/codex/device"
                target="_blank"
                rel="noopener noreferrer"
                variant="outline">Open approval page<ExternalLink aria-hidden="true" /></Button
              >{/if}
          </div>
        </li>
        <li>
          <span class="device-step" aria-hidden="true">3</span>
          <div>
            <h3>Return here when approved</h3>
            <p class="device-muted">
              This page checks automatically. You can navigate away and resume this request when you
              return.
            </p>
          </div>
        </li>
      </ol>
    {:else if flow.status === 'expired'}
      <Alert.Root role="status"
        ><Alert.Title>The approval code expired</Alert.Title><Alert.Description
          >No new account was connected. Start again to get a fresh code.</Alert.Description
        ></Alert.Root
      >
    {:else if flow.status === 'error'}
      <Alert.Root role="alert"
        ><Alert.Title>The account could not connect</Alert.Title><Alert.Description
          >{flow.error || 'Start again to request a new approval code.'}</Alert.Description
        ></Alert.Root
      >
    {:else if flow.status === 'cancelled'}
      <p role="status" class="device-muted">
        Connection request cancelled. You can connect an account whenever you are ready.
      </p>
    {:else}
      <p class="device-intro">
        No credential files or local commands needed. Get a one-time code, approve access, and your
        account will appear in credentials.
      </p>
    {/if}
    {#if !complete}
      <p class="device-help">
        If device authentication is disabled, enable it in your ChatGPT security settings, then
        request a new code. Use an account with access to Codex.
      </p>
    {/if}
  </Card.Content>
  <Card.Footer class="device-actions">
    {#if pending}
      <Button variant="outline" onclick={() => perform('load')} disabled={disabled || !!busy}
        >{#if busy === 'load'}<LoaderCircle class="animate-spin" aria-hidden="true" />{/if}Check
        connection</Button
      >
      <Button variant="ghost" onclick={() => perform('cancel')} disabled={disabled || !!busy}
        >{busy === 'cancel' ? 'Cancelling…' : 'Cancel connection'}</Button
      >
    {:else if uncertain || !flow}
      <Button variant="outline" onclick={() => perform('load')} disabled={disabled || !!busy}
        >Check connection</Button
      >
    {:else}
      <Button onclick={() => perform('start')} disabled={disabled || !!busy}
        >{#if busy === 'start'}<LoaderCircle
            class="animate-spin"
            aria-hidden="true"
          />{/if}{busy === 'start'
          ? 'Requesting code…'
          : complete
            ? 'Connect another account'
            : flow.status === 'idle'
              ? 'Connect Codex account'
              : 'Try again'}</Button
      >
    {/if}
    {#if complete && refreshFailed}<Button
        variant="outline"
        onclick={refreshAccounts}
        disabled={disabled || !!busy}>Refresh credentials</Button
      >{/if}
    <span role="status" class="device-muted"
      >{copied && pending ? 'Code copied to clipboard.' : ''}</span
    >
  </Card.Footer>
</Card.Root>

<style>
  :global(.device-auth) {
    container-type: inline-size;
  }
  .device-heading {
    display: flex;
    align-items: flex-start;
    gap: 0.85rem;
    flex-wrap: wrap;
  }
  .device-heading > .min-w-0 {
    flex: 1 1 15rem;
  }
  .device-symbol {
    display: grid;
    place-items: center;
    width: 2.6rem;
    height: 2.6rem;
    flex: 0 0 auto;
    border: 1px solid var(--border);
    border-radius: 0.75rem;
    background: var(--muted);
  }
  :global(.device-content) {
    display: grid;
    gap: 1.2rem;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .device-intro {
    max-width: 42rem;
    font-size: 0.9rem;
    line-height: 1.6;
  }
  .device-muted,
  .device-help {
    color: var(--muted-foreground);
    font-size: 0.82rem;
    line-height: 1.6;
  }
  .device-help {
    border-top: 1px solid var(--border);
    padding-top: 1rem;
  }
  .device-steps {
    display: grid;
    gap: 1.4rem;
    padding: 0;
    list-style: none;
  }
  .device-steps li {
    display: grid;
    grid-template-columns: 1.6rem minmax(0, 1fr);
    gap: 0.75rem;
  }
  .device-step {
    display: grid;
    place-items: center;
    height: 1.6rem;
    border: 1px solid var(--border);
    border-radius: 100%;
    font-size: 0.75rem;
  }
  h3 {
    font-size: 0.88rem;
    font-weight: 600;
    line-height: 1.6;
    margin-bottom: 0.25rem;
  }
  .device-code {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin: 0.8rem 0 0.4rem;
    padding: 1rem;
    border: 1px solid var(--border);
    border-radius: 0.65rem;
    background: var(--muted);
  }
  output {
    flex: 1 1 9rem;
    min-width: 0;
  }
  code {
    display: block;
    font-size: 1.45rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    overflow-wrap: anywhere;
    user-select: all;
  }
  .device-steps li:nth-child(2) .device-muted {
    margin-bottom: 0.7rem;
  }
  .device-success {
    display: flex;
    gap: 0.7rem;
    align-items: flex-start;
  }
  .device-success > :global(svg) {
    flex-shrink: 0;
    margin-top: 0.2rem;
  }
  .device-success > div {
    min-width: 0;
  }
  :global(.device-actions) {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem;
  }
  @container (max-width: 350px) {
    :global(.device-actions > button),
    :global(.device-steps a) {
      white-space: normal;
      height: auto;
      min-height: 2rem;
      text-align: center;
    }
    .device-code {
      padding: 0.7rem;
    }
  }
</style>
