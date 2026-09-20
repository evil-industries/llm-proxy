import { MockEventSource } from './realtime-fixture';
import { expect, test, vi } from 'vitest';
import { commands, page, userEvent } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import CodexDeviceAuth from '$lib/components/management/CodexDeviceAuth.svelte';
import type { ManagementClient, CodexDeviceAuth as DeviceAuth } from '$lib/api';

const pending = (overrides: Partial<DeviceAuth> = {}): DeviceAuth => ({
  status: 'pending',
  user_code: 'ABCD-EFGH',
  verification_uri: 'https://auth.openai.com/codex/device',
  expires_at: new Date(Date.now() + 600_000).toISOString(),
  interval: 3600,
  ...overrides
});
function fixture(initial: DeviceAuth = { status: 'idle' }) {
  const state = { value: initial };
  const get = vi.fn(async (_signal?: AbortSignal) => structuredClone(state.value));
  const start = vi.fn(async (_signal?: AbortSignal) => structuredClone((state.value = pending())));
  const cancel = vi.fn(async (_signal?: AbortSignal): Promise<DeviceAuth> =>
    structuredClone((state.value = { status: 'cancelled' }))
  );
  const client = {
    getCodexDeviceAuth: get,
    startCodexDeviceAuth: start,
    cancelCodexDeviceAuth: cancel
  } as unknown as ManagementClient;
  return { client, state, get, start, cancel };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (cause?: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

test('starts only on request, copies the code, uses a fixed approval link, and refreshes the connected account', async () => {
  const { client, start, state } = fixture();
  const connected = vi.fn(async () => {});
  const copy = vi.fn(async () => {});
  vi.spyOn(navigator.clipboard, 'writeText').mockImplementation(copy);
  render(CodexDeviceAuth, { client, onconnected: connected });
  await expect
    .element(page.getByRole('button', { name: 'Connect Codex account', exact: true }))
    .toBeEnabled();
  expect(start).not.toHaveBeenCalled();
  await page.getByRole('button', { name: 'Connect Codex account', exact: true }).click();
  await expect.element(page.getByLabelText('One-time device code')).toHaveTextContent('ABCD-EFGH');
  await page.getByRole('button', { name: 'Copy code', exact: true }).click();
  expect(copy).toHaveBeenCalledWith('ABCD-EFGH');
  await expect.element(page.getByText('Code copied to clipboard.')).toBeVisible();
  const approval = page.getByRole('link', { name: 'Open approval page' });
  await expect.element(approval).toHaveAttribute('href', 'https://auth.openai.com/codex/device');
  await expect.element(approval).toHaveAttribute('rel', 'noopener noreferrer');
  state.value = {
    status: 'complete',
    account: { name: 'Operator', email: 'operator@example.com', plan_type: 'Plus' }
  };
  await page.getByRole('button', { name: 'Check connection' }).click();
  await expect.element(page.getByText('Codex account connected', { exact: true })).toBeVisible();
  await expect.element(page.getByText('operator@example.com')).toBeVisible();
  expect(connected).toHaveBeenCalledTimes(1);
  await page.getByRole('button', { name: 'Connect another account' }).click();
  expect(start).toHaveBeenCalledTimes(2);
});

test('resumes pending approval without starting again and preserves it across transient failures', async () => {
  const { client, get, start } = fixture(
    pending({ verification_uri: 'https://attacker.invalid/' })
  );
  render(CodexDeviceAuth, { client });
  await expect.element(page.getByLabelText('One-time device code')).toBeVisible();
  get.mockRejectedValueOnce(new Error('private-server-token'));
  await page.getByRole('button', { name: 'Check connection' }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent('approval request has been kept');
  await expect.element(page.getByLabelText('One-time device code')).toHaveTextContent('ABCD-EFGH');
  expect(document.body.textContent).not.toContain('private-server-token');
  await expect
    .element(page.getByRole('link', { name: 'Open approval page' }))
    .toHaveAttribute('href', 'https://auth.openai.com/codex/device');
  await page.getByRole('button', { name: 'Check connection' }).click();
  await expect.element(page.getByRole('alert')).not.toBeInTheDocument();
  expect(start).not.toHaveBeenCalled();
});

test('does not overlap status requests and only aborts the view when unmounted', async () => {
  const { client, get, cancel } = fixture(pending({ interval: 1 }));
  const view = await render(CodexDeviceAuth, { client });
  await expect.element(page.getByLabelText('One-time device code')).toBeVisible();
  const next = deferred<DeviceAuth>();
  get.mockImplementationOnce(() => next.promise);
  MockEventSource.emit(16);
  await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  await expect.element(page.getByRole('button', { name: 'Check connection' })).toBeDisabled();
  const signal = get.mock.calls[1][0];
  await view.unmount();
  expect(signal?.aborted).toBe(true);
  expect(cancel).not.toHaveBeenCalled();
  next.resolve({ status: 'complete' });
  expect(get).toHaveBeenCalledTimes(2);
  render(CodexDeviceAuth, { client });
  await expect.element(page.getByLabelText('One-time device code')).toBeVisible();
});

test('reconciles a lost start response before allowing another start', async () => {
  const { client, start, state } = fixture();
  start.mockImplementationOnce(async () => {
    state.value = pending();
    throw new Error('lost response');
  });
  render(CodexDeviceAuth, { client });
  await page.getByRole('button', { name: 'Connect Codex account', exact: true }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent('Checking its status');
  await expect
    .element(page.getByRole('button', { name: 'Connect Codex account', exact: true }))
    .not.toBeInTheDocument();
  await page.getByRole('button', { name: 'Check connection' }).click();
  await expect.element(page.getByLabelText('One-time device code')).toBeVisible();
  expect(start).toHaveBeenCalledTimes(1);
});

test('shows expiry without inventing success, then lets the server confirm expiry before retrying', async () => {
  const { client, state, start } = fixture(
    pending({ expires_at: new Date(Date.now() - 1000).toISOString() })
  );
  render(CodexDeviceAuth, { client });
  await expect.element(page.getByRole('timer')).toHaveTextContent('code has expired');
  await expect.element(page.getByRole('button', { name: 'Copy code', exact: true })).toBeDisabled();
  await expect
    .element(page.getByRole('link', { name: 'Open approval page' }))
    .not.toBeInTheDocument();
  state.value = { status: 'expired' };
  await page.getByRole('button', { name: 'Check connection' }).click();
  await expect.element(page.getByText('The approval code expired', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Try again' }).click();
  expect(start).toHaveBeenCalledTimes(1);
  await expect.element(page.getByRole('timer')).toHaveTextContent('Code expires in');
});

test('honors completion winning a cancellation race and keeps success if list refresh fails', async () => {
  const { client, cancel } = fixture(pending());
  cancel.mockResolvedValueOnce({ status: 'complete', account: { name: 'Operator' } });
  const connected = vi
    .fn()
    .mockRejectedValueOnce(new Error('list unavailable'))
    .mockResolvedValue(undefined);
  render(CodexDeviceAuth, { client, onconnected: connected });
  await page.getByRole('button', { name: 'Cancel connection' }).click();
  await expect.element(page.getByText('Codex account connected', { exact: true })).toBeVisible();
  await expect
    .element(page.getByRole('alert'))
    .toHaveTextContent('credential list could not refresh');
  await page.getByRole('button', { name: 'Refresh credentials' }).click();
  await expect.element(page.getByRole('alert')).not.toBeInTheDocument();
  expect(connected).toHaveBeenCalledTimes(2);
});

test('retains a pending request if cancellation fails, then confirms cancellation', async () => {
  const { client, cancel } = fixture(pending());
  cancel.mockRejectedValueOnce(new Error('offline'));
  render(CodexDeviceAuth, { client });
  await page.getByRole('button', { name: 'Cancel connection' }).click();
  await expect
    .element(page.getByRole('alert'))
    .toHaveTextContent('Cancellation could not be confirmed');
  await expect.element(page.getByLabelText('One-time device code')).toBeVisible();
  await page.getByRole('button', { name: 'Cancel connection' }).click();
  await expect
    .element(page.getByText('Connection request cancelled.', { exact: false }))
    .toBeVisible();
  await expect.element(page.getByRole('button', { name: 'Try again' })).toBeEnabled();
});

test('offers a safe manual copy fallback and explains disabled device authentication', async () => {
  const { client, state } = fixture(pending());
  vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('denied'));
  render(CodexDeviceAuth, { client });
  await page.getByRole('button', { name: 'Copy code', exact: true }).click();
  await expect.element(page.getByText('Copy is unavailable.', { exact: false })).toBeVisible();
  state.value = { status: 'error', error: 'Device authentication is disabled.' };
  await page.getByRole('button', { name: 'Check connection' }).click();
  await expect
    .element(page.getByRole('alert'))
    .toHaveTextContent('Device authentication is disabled.');
  await expect
    .element(page.getByText('If device authentication is disabled', { exact: false }))
    .toBeVisible();
  await expect.element(page.getByRole('button', { name: 'Try again' })).toBeEnabled();
});

test('wraps long codes and account identities at phone width and enlarged text', async () => {
  await page.viewport(320, 900);
  const previous = document.documentElement.style.fontSize;
  document.documentElement.style.fontSize = '20px';
  const { client, state } = fixture(pending({ user_code: 'LONG-CODE-'.repeat(24) }));
  try {
    render(CodexDeviceAuth, { client });
    await expect.element(page.getByLabelText('One-time device code')).toBeVisible();
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(window.innerWidth + 1);
    const code = document.querySelector('code')!;
    expect(code.scrollWidth).toBeLessThanOrEqual(code.clientWidth + 1);
    state.value = {
      status: 'complete',
      account: {
        name: 'Operator',
        email: `${'long-account-'.repeat(30)}@example.com`,
        plan_type: 'Long plan '.repeat(20)
      }
    };
    await page.getByRole('button', { name: 'Check connection' }).click();
    await expect.element(page.getByText('Codex account connected', { exact: true })).toBeVisible();
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(window.innerWidth + 1);
  } finally {
    document.documentElement.style.fontSize = previous;
    await page.viewport(1280, 900);
  }
});

test('respects disabled setup controls', async () => {
  const { client, start } = fixture();
  render(CodexDeviceAuth, { client, disabled: true });
  await expect
    .element(page.getByRole('button', { name: 'Connect Codex account', exact: true }))
    .toBeDisabled();
  expect(start).not.toHaveBeenCalled();
});

test('provides accessible approval instructions and keyboard-operable controls', async () => {
  const { client } = fixture();
  await render(CodexDeviceAuth, { client });
  const start = page.getByRole('button', { name: 'Connect Codex account', exact: true });
  await expect.element(start).toBeEnabled();
  start.element().focus();
  await userEvent.keyboard('{Enter}');
  await expect.element(page.getByLabelText('One-time device code')).toBeVisible();
  page
    .getByRole('region', { name: 'Connect a Codex account' })
    .element()
    .classList.add('app-shell');
  expect(await commands.auditAccessibility()).toEqual([]);
});
