import { describe, expect, test, vi } from 'vitest';
import { page, server, userEvent } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import ManagementApp from '$lib/components/ManagementApp.svelte';
import LoginPanel from '$lib/components/LoginPanel.svelte';
import LoginPage from '../routes/login/+page.svelte';
import { demoConfig, demoFiles, demoNotifications } from '$lib/demo';

const sections = ['Overview', 'Credentials', 'API keys', 'Logs', 'Settings'] as const;
const longName = `production-${'credential'.repeat(24)}.json`;
const longKey = `sk-${'long-key'.repeat(80)}`;
const secret = 'management-test-secret-never-persist';

function mockServer() {
  const state = {
    files: [
      {
        ...demoFiles[0],
        name: longName,
        email: `${'account'.repeat(32)}@example.com`,
        status_message: 'Upstream status: ' + 'a'.repeat(280),
        auth_index: 'auth-test'
      }
    ],
    keys: [longKey],
    config: structuredClone(demoConfig),
    failRefresh: false,
    sessionExpired: false,
    failMutation: false,
    lines: ['[info] ' + 'long-log-value'.repeat(100), '[error] credential unavailable']
  };
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const url = new URL(String(input), window.location.origin);
    const method = init?.method ?? 'GET';
    const path = url.pathname.replace('/api/management', '');
    if (url.pathname === '/api/session' && method === 'DELETE')
      return new Response(null, { status: 204 });
    if (state.sessionExpired) return Response.json({ error: secret }, { status: 401 });
    if (url.pathname === '/api/codex/device-auth' && method === 'GET')
      return Response.json({ status: 'idle' });
    if (state.failRefresh && method === 'GET')
      return Response.json({ error: secret }, { status: 503 });
    if (state.failMutation && method !== 'GET')
      return Response.json({ error: secret }, { status: 503 });
    if (path === '/config') return Response.json(state.config);
    if (path === '/notifications') return Response.json(demoNotifications);
    if (path === '/auth-files/status' && method === 'PATCH') {
      const body = JSON.parse(String(init?.body));
      state.files[0].disabled = body.disabled;
      return Response.json({ status: 'ok' });
    }
    if (path === '/auth-files') {
      if (method === 'DELETE')
        state.files = state.files.filter((file) => file.name !== url.searchParams.get('name'));
      if (method === 'POST') {
        const file = (init?.body as FormData).get('file') as File;
        state.files.push({ ...state.files[0], name: file.name, auth_index: 'auth-uploaded' });
      }
      return Response.json({ files: state.files });
    }
    if (path === '/routing/strategy' && method === 'PUT') {
      state.config.routing = { strategy: JSON.parse(String(init?.body)).value };
      return Response.json({ status: 'ok' });
    }
    if (path === '/debug' && method === 'PUT') {
      state.config.debug = JSON.parse(String(init?.body)).value;
      return Response.json({ status: 'ok' });
    }
    if (path === '/api-keys/mutate' && method === 'POST') {
      const { action, value } = JSON.parse(String(init?.body));
      const key = value.trim();
      const exists = state.keys.some((item) => item.trim() === key);
      if (action === 'add') {
        if (exists) return Response.json({ error: 'key exists' }, { status: 409 });
        state.keys.push(key);
      } else if (action === 'remove') {
        const remaining = state.keys.filter((item) => item.trim() !== key);
        if (!exists || !remaining.some((item) => item.trim()))
          return Response.json({ error: 'key conflict' }, { status: 409 });
        state.keys = remaining;
      } else {
        return Response.json({ error: 'invalid action' }, { status: 400 });
      }
      return Response.json({ status: 'ok' });
    }
    if (path === '/api-keys' && method === 'GET') return Response.json({ 'api-keys': state.keys });
    if (path === '/logs')
      return Response.json({
        lines: state.lines,
        'line-count': state.lines.length,
        'latest-timestamp': 1726500000,
        'next-cursor': 'test-cursor'
      });
    throw new Error(`Unexpected test request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetch);
  return { state, fetch };
}

async function loaded() {
  await expect.element(page.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible();
  await expect.element(page.getByRole('button', { name: 'Refresh data' })).toBeEnabled();
  await expect.element(page.getByRole('heading', { name: 'Connected providers' })).toBeVisible();
}

async function navigate(section: (typeof sections)[number]) {
  await page
    .getByRole('navigation', { name: 'Management sections' })
    .getByRole('button', { name: section, exact: true })
    .click();
  await expect.element(page.getByRole('heading', { name: section, level: 1 })).toBeVisible();
  await expect.element(page.getByRole('button', { name: 'Refresh data' })).toBeEnabled();
}

/** Text-only zoom catches fixed-height clipping even when styles use px instead of rem. */
function enlargeText() {
  const elements = [...document.querySelectorAll<HTMLElement>('.app-shell, .app-shell *')];
  const sizes = elements.map((element) => parseFloat(getComputedStyle(element).fontSize));
  elements.forEach((element, index) => {
    element.style.fontSize = `${sizes[index] * 2}px`;
  });
}

async function assertNoClipping(label: string) {
  // Let ResizeObserver-driven charts respond to the changed font-relative gutter.
  await new Promise<void>((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  );
  const root = document.querySelector<HTMLElement>('.app-shell')!;
  const failures: string[] = [];
  const tolerance = 2;
  expect
    .soft(document.documentElement.scrollWidth, `${label}: horizontal page overflow`)
    .toBeLessThanOrEqual(window.innerWidth + tolerance);
  for (const element of root.querySelectorAll<HTMLElement>('*')) {
    const style = getComputedStyle(element);
    if (
      !element.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }) ||
      element.closest('.sr-only, .skip-link, svg') ||
      element instanceof SVGElement
    )
      continue;
    const rect = element.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) continue;
    // Inputs scroll their value; log output is a keyboard-focusable scroll region.
    // shadcn switches deliberately extend their pseudo-element hit target beyond their track.
    const scrolling = element.closest('[aria-label="Log output"], .table-scroll');
    if (
      !scrolling &&
      !element.matches('input, select, option, [data-slot="switch"], .lc-tooltip-context')
    ) {
      if (element.clientWidth && element.scrollWidth > element.clientWidth + tolerance)
        failures.push(`${element.tagName}.${element.classList[0]} overflows horizontally`);
      if (
        element.clientHeight &&
        element.scrollHeight > element.clientHeight + tolerance &&
        !['auto', 'scroll'].includes(style.overflowY)
      )
        failures.push(`${element.tagName}.${element.classList[0]} clips vertically`);
    }
    if (element.matches('button, input, select, a, [role="switch"]') && !scrolling) {
      if (rect.left < -tolerance || rect.right > window.innerWidth + tolerance)
        failures.push(
          `${element.getAttribute('aria-label') || element.textContent?.trim()} control offscreen`
        );
      if (!element.matches(':disabled') && style.pointerEvents !== 'none') {
        element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' });
        const visible = element.getBoundingClientRect();
        const hit = document.elementFromPoint(
          visible.left + visible.width / 2,
          visible.top + visible.height / 2
        );
        if (!hit || !element.contains(hit))
          failures.push(
            `${element.getAttribute('aria-label') || element.textContent?.trim()} control is covered or unreachable`
          );
      }
    }
    for (const node of element.childNodes) {
      if (node.nodeType !== Node.TEXT_NODE || !node.textContent?.trim()) continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      for (const textRect of range.getClientRects()) {
        for (
          let ancestor: HTMLElement | null = element;
          ancestor && root.contains(ancestor);
          ancestor = ancestor.parentElement
        ) {
          if (ancestor.closest('[aria-label="Log output"], .table-scroll')) break;
          const css = getComputedStyle(ancestor);
          const bounds = ancestor.getBoundingClientRect();
          if (
            ['hidden', 'clip'].includes(css.overflowX) &&
            (textRect.left < bounds.left - tolerance || textRect.right > bounds.right + tolerance)
          )
            failures.push(`${element.tagName} text clipped horizontally`);
          if (
            ['hidden', 'clip'].includes(css.overflowY) &&
            (textRect.top < bounds.top - tolerance || textRect.bottom > bounds.bottom + tolerance)
          )
            failures.push(`${element.tagName} text clipped vertically`);
        }
      }
    }
  }
  // LayerChart wraps each label in its own tightly fitted SVG. Compare with the
  // chart panel and viewport, not that per-label SVG, to catch clipped leading digits.
  for (const text of root.querySelectorAll<SVGTextElement>('.request-chart svg text')) {
    if (!text.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })) continue;
    const bounds = text.closest('.request-chart')!.getBoundingClientRect();
    const rect = text.getBoundingClientRect();
    if (
      rect.left < Math.max(0, bounds.left) - tolerance ||
      rect.right > Math.min(window.innerWidth, bounds.right) + tolerance ||
      rect.top < bounds.top - tolerance ||
      rect.bottom > bounds.bottom + tolerance
    )
      failures.push(`Chart label ${text.textContent} exceeds chart panel or viewport`);
  }
  expect.soft([...new Set(failures)], label).toEqual([]);
}

describe('responsive management screens in Chromium', () => {
  for (const width of [320, 390, 768, 1440]) {
    for (const zoom of [1, 2]) {
      test(`${width}px viewport, ${zoom * 100}% text: sign in and every section remain readable`, async () => {
        await page.viewport(width, 900);
        mockServer();
        const login = await render(LoginPage);
        if (zoom === 2) enlargeText();
        await assertNoClipping('Sign in');
        await login.unmount();
        await render(ManagementApp);
        await loaded();
        for (const section of sections) {
          await navigate(section);
          if (section === 'Logs')
            await expect.element(page.getByRole('region', { name: 'Log output' })).toBeVisible();
          if (section === 'API keys')
            await page.getByRole('button', { name: 'Reveal key 1', exact: true }).click();
          if (section === 'Settings')
            await expect
              .element(page.getByLabelText('Warning remaining quota (%)'))
              .toHaveValue(20);
          if (zoom === 2) enlargeText();
          await assertNoClipping(section);
          document
            .querySelectorAll<HTMLElement>('[style]')
            .forEach((el) => el.style.removeProperty('font-size'));
        }
      });
    }
  }
});

test('loads through the session relay without exposing a backend key and signs out', async () => {
  await page.viewport(1440, 900);
  const { fetch } = mockServer();
  const storage = vi.spyOn(Storage.prototype, 'setItem');
  const onlogout = vi.fn();
  await render(ManagementApp, { onlogout });
  await loaded();
  expect(fetch.mock.calls.length).toBeGreaterThanOrEqual(3);
  for (const [url, options] of fetch.mock.calls) {
    expect(String(url)).toMatch(/^\/api\/management\//);
    expect(new Headers(options?.headers).has('Authorization')).toBe(false);
    expect(options?.credentials).toBe('same-origin');
  }
  expect(storage).not.toHaveBeenCalled();
  expect(document.body.textContent).not.toMatch(
    /CLIProxyAPI|One endpoint|Your models|management key/i
  );
  await navigate('API keys');
  await page.getByRole('button', { name: 'Reveal key 1', exact: true }).click();
  expect(document.body.textContent).toContain(longKey);
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect.poll(() => onlogout.mock.calls.length).toBe(1);
  expect(fetch).toHaveBeenLastCalledWith('/api/session', {
    method: 'DELETE',
    credentials: 'same-origin'
  });
  expect(document.body.textContent).not.toContain(longKey);
  await expect.element(page.getByRole('heading', { name: 'Session ended' })).toBeVisible();
});

test('session expiry clears loaded credentials and revealed keys and requires sign-in', async () => {
  const { state } = mockServer();
  await render(ManagementApp);
  await loaded();
  await navigate('API keys');
  await page.getByRole('button', { name: 'Reveal key 1', exact: true }).click();
  expect(document.body.textContent).toContain(longKey);
  state.sessionExpired = true;
  await page.getByRole('button', { name: 'Refresh data' }).click();
  await expect.element(page.getByRole('heading', { name: 'Session ended' })).toBeVisible();
  await expect
    .element(page.getByRole('link', { name: 'Sign in', exact: true }))
    .toHaveAttribute('href', '/login');
  expect(document.body.textContent).not.toContain(longKey);
  expect(document.body.textContent).not.toContain(longName);
  expect(document.body.textContent).not.toContain(secret);
});

test('failed refresh retains loaded data and exposes a safe actionable error', async () => {
  const { state } = mockServer();
  await render(ManagementApp);
  await loaded();
  await navigate('Credentials');
  state.failRefresh = true;
  await page.getByRole('button', { name: 'Refresh data' }).click();
  await expect
    .element(page.getByRole('alert'))
    .toHaveTextContent('Previously loaded data may be out of date.');
  await expect.element(page.getByText(longName, { exact: true })).toBeVisible();
  expect(document.body.textContent).not.toContain(secret);
  state.failRefresh = false;
  await page.getByRole('button', { name: 'Refresh data' }).click();
  await expect.element(page.getByRole('alert')).not.toBeInTheDocument();
});

test('adds a key and requires explicit confirmation before deleting it', async () => {
  const { state, fetch } = mockServer();
  await render(ManagementApp);
  await loaded();
  await navigate('API keys');
  await page.getByLabelText('Add an API key', { exact: true }).fill('new-client-key');
  await page.getByRole('button', { name: 'Add key', exact: true }).click();
  await expect.element(page.getByRole('status')).toHaveTextContent('API key added.');
  expect(state.keys).toEqual([longKey, 'new-client-key']);
  await page.getByRole('button', { name: 'Delete key 2', exact: true }).click();
  expect(
    fetch.mock.calls.filter(
      ([url, options]) =>
        String(url).endsWith('/api-keys/mutate') &&
        JSON.parse(String(options?.body)).action === 'remove'
    )
  ).toHaveLength(0);
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(state.keys).toHaveLength(2);
  await page.getByRole('button', { name: 'Delete key 2', exact: true }).click();
  await page.getByRole('button', { name: 'Delete key', exact: true }).click();
  await expect.element(page.getByRole('status')).toHaveTextContent('API key deleted.');
  expect(state.keys).toEqual([longKey]);
});

test('credential switch persists through the actual client and refreshes its state', async () => {
  const { state, fetch } = mockServer();
  await render(ManagementApp);
  await loaded();
  await navigate('Credentials');
  await page.getByRole('switch', { name: `Enable ${longName}`, exact: true }).click();
  await expect.element(page.getByText('Credential disabled.', { exact: true })).toBeVisible();
  expect(state.files[0].disabled).toBe(true);
  const patch = fetch.mock.calls.find(([, options]) => options?.method === 'PATCH');
  expect(JSON.parse(String(patch?.[1]?.body))).toEqual({
    name: longName,
    disabled: true,
    auth_index: 'auth-test'
  });
  await expect
    .element(page.getByRole('switch', { name: `Enable ${longName}`, exact: true }))
    .not.toBeChecked();
});

test('supports keyboard skip link and section navigation with visible focus', async () => {
  await page.viewport(390, 900);
  mockServer();
  await render(ManagementApp);
  (document.activeElement as HTMLElement)?.blur();
  await userEvent.tab();
  await expect.element(page.getByRole('link', { name: 'Skip to content' })).toHaveFocus();
  await userEvent.keyboard('{Enter}');
  await expect.element(page.getByRole('main')).toHaveFocus();
  await loaded();
  const nav = page.getByRole('navigation', { name: 'Management sections' });
  await nav.getByRole('button', { name: 'Overview', exact: true }).click();
  await userEvent.tab();
  await expect.element(nav.getByRole('button', { name: 'Credentials', exact: true })).toHaveFocus();
  expect(getComputedStyle(document.activeElement!).outlineStyle).not.toBe('none');
  await userEvent.keyboard('{Enter}');
  await expect.element(page.getByRole('heading', { name: 'Credentials', level: 1 })).toBeVisible();
});

test('sign in and all management sections pass automated WCAG accessibility checks', async () => {
  await page.viewport(390, 900);
  mockServer();
  const login = await render(LoginPage);
  expect(await server.commands.auditAccessibility(), 'Sign in').toEqual([]);
  await login.unmount();
  await render(ManagementApp);
  await loaded();
  for (const section of sections) {
    await navigate(section);
    if (section === 'Settings')
      await expect.element(page.getByLabelText('Warning remaining quota (%)')).toHaveValue(20);
    if (section === 'Logs') {
      await expect
        .element(page.getByRole('button', { name: 'Refresh logs', exact: true }))
        .toBeEnabled();
      // Wait for initial loading-button opacity transitions before measuring contrast.
      await expect
        .poll(() =>
          [
            ...document.querySelectorAll<HTMLElement>(
              '[aria-label="Log inspection"] button:not(:disabled)'
            )
          ].every((element) => getComputedStyle(element).opacity === '1')
        )
        .toBe(true);
    }
    expect(await server.commands.auditAccessibility(), section).toEqual([]);
  }
});

test('demo navigation stays read-only without making server requests', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>();
  vi.stubGlobal('fetch', fetch);
  await render(ManagementApp, { initialDemo: true });
  await navigate('Credentials');
  await expect
    .element(page.getByRole('button', { name: 'Upload JSON', exact: true }))
    .toBeDisabled();
  await navigate('API keys');
  await expect.element(page.getByLabelText('Add an API key', { exact: true })).toBeDisabled();
  await navigate('Settings');
  await expect.element(page.getByRole('combobox', { name: 'Routing strategy' })).toBeDisabled();
  await navigate('Logs');
  await page.getByRole('textbox', { name: 'Search logs' }).fill('no-matching-event');
  await expect.element(page.getByRole('heading', { name: 'No matching log lines' })).toBeVisible();
  expect(fetch).not.toHaveBeenCalled();
});

test('settings save routing and booleans, preserving a draft after a failed save', async () => {
  const { state, fetch } = mockServer();
  await render(ManagementApp);
  await loaded();
  await navigate('Settings');
  const routing = page.getByRole('combobox', { name: 'Routing strategy' });
  await routing.selectOptions('fill-first');
  await page.getByRole('button', { name: 'Save routing', exact: true }).click();
  await expect.element(page.getByRole('status')).toHaveTextContent('Routing strategy saved.');
  expect(state.config.routing?.strategy).toBe('fill-first');
  await expect
    .element(page.getByRole('button', { name: 'Save routing', exact: true }))
    .toBeDisabled();
  await page.getByRole('switch', { name: 'Debug mode', exact: true }).click();
  state.failMutation = true;
  await page.getByRole('button', { name: 'Save debug mode', exact: true }).click();
  await expect
    .element(page.getByRole('alert'))
    .toHaveTextContent('The server could not complete this request.');
  expect(state.config.debug).toBe(false);
  await expect.element(page.getByRole('switch', { name: 'Debug mode', exact: true })).toBeChecked();
  state.failMutation = false;
  await page.getByRole('button', { name: 'Save debug mode', exact: true }).click();
  await expect.element(page.getByRole('status')).toHaveTextContent('Debug mode saved.');
  expect(state.config.debug).toBe(true);
  expect(fetch.mock.calls.filter(([url]) => String(url).endsWith('/debug'))).toHaveLength(2);
});

test('uploads a credential as multipart and confirms removal before deleting', async () => {
  const { state, fetch } = mockServer();
  await render(ManagementApp);
  await loaded();
  await navigate('Credentials');
  const file = new File(['{"type":"codex","access_token":"test-only"}'], 'uploaded-account.json', {
    type: 'application/json'
  });
  await page.getByLabelText('Upload credential JSON', { exact: true }).upload(file);
  await expect.element(page.getByText('Credential uploaded.', { exact: true })).toBeVisible();
  const upload = fetch.mock.calls.find(([, options]) => options?.method === 'POST');
  expect((upload?.[1]?.body as FormData).get('file')).toBeInstanceOf(File);
  expect(new Headers(upload?.[1]?.headers).has('Content-Type')).toBe(false);
  await expect.element(page.getByText('uploaded-account.json', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Delete uploaded-account.json', exact: true }).click();
  expect(state.files).toHaveLength(2);
  expect(fetch.mock.calls.some(([, options]) => options?.method === 'DELETE')).toBe(false);
  await page.getByRole('button', { name: 'Delete credential', exact: true }).click();
  await expect.element(page.getByText('Credential deleted.', { exact: true })).toBeVisible();
  expect(state.files).toHaveLength(1);
  await expect
    .element(page.getByText('uploaded-account.json', { exact: true }))
    .not.toBeInTheDocument();
});

test('password sign-in submits only to the session endpoint and clears the password on success', async () => {
  const fetch = vi
    .fn<typeof globalThis.fetch>()
    .mockResolvedValue(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetch);
  const storage = vi.spyOn(Storage.prototype, 'setItem');
  const onauthenticated = vi.fn();
  await render(LoginPanel, { onauthenticated });
  await expect.element(page.getByRole('button', { name: 'Sign in', exact: true })).toBeDisabled();
  await page.getByLabelText('Password', { exact: true }).fill(secret);
  await userEvent.keyboard('{Enter}');
  await expect.poll(() => onauthenticated.mock.calls.length).toBe(1);
  expect(fetch).toHaveBeenCalledExactlyOnceWith('/api/session', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify({ password: secret })
  });
  expect(storage).not.toHaveBeenCalled();
  await expect.element(page.getByLabelText('Password', { exact: true })).toHaveValue('');
  expect(document.body.textContent).not.toContain(secret);
  expect(document.body.textContent).not.toMatch(/CLIProxyAPI|management key|demo/i);
});

test.each([
  [401, 'Incorrect password.'],
  [429, 'Too many sign-in attempts. Try again later.'],
  [503, 'Sign-in is temporarily unavailable. Try again shortly.'],
  [500, 'Unable to sign in. Please try again.']
])('sign-in handles HTTP %i without exposing response contents', async (status, message) => {
  const fetch = vi
    .fn<typeof globalThis.fetch>()
    .mockResolvedValue(Response.json({ error: secret }, { status }));
  vi.stubGlobal('fetch', fetch);
  const onauthenticated = vi.fn();
  await render(LoginPanel, { onauthenticated });
  await page.getByLabelText('Password', { exact: true }).fill(secret);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent(message);
  await expect.element(page.getByRole('button', { name: 'Sign in', exact: true })).toBeEnabled();
  expect(onauthenticated).not.toHaveBeenCalled();
  expect(document.body.textContent).not.toContain(secret);
});

test.each([
  ['not_configured', 'Sign-in is not configured. Contact your administrator.'],
  ['temporarily_unavailable', 'Sign-in is temporarily unavailable. Try again shortly.'],
  ['unknown', 'Sign-in is temporarily unavailable. Try again shortly.']
])('sign-in distinguishes 503 code %s without showing server details', async (code, message) => {
  const fetch = vi
    .fn<typeof globalThis.fetch>()
    .mockResolvedValueOnce(Response.json({ code, error: secret }, { status: 503 }))
    .mockResolvedValueOnce(Response.json({ authenticated: true }));
  vi.stubGlobal('fetch', fetch);
  const onauthenticated = vi.fn();
  await render(LoginPanel, { onauthenticated });
  await page.getByLabelText('Password', { exact: true }).fill(secret);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect.element(page.getByRole('alert')).toHaveTextContent(message);
  expect(document.body.textContent).not.toContain(secret);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect.poll(() => onauthenticated.mock.calls.length).toBe(1);
});

test('a failed sign-in request allows retry without persisting the password', async () => {
  const fetch = vi
    .fn<typeof globalThis.fetch>()
    .mockRejectedValueOnce(new TypeError('offline'))
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetch);
  const onauthenticated = vi.fn();
  const storage = vi.spyOn(Storage.prototype, 'setItem');
  await render(LoginPanel, { onauthenticated });
  await page.getByLabelText('Password', { exact: true }).fill(secret);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect
    .element(page.getByRole('alert'))
    .toHaveTextContent('Unable to reach the server. Please try again.');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect.poll(() => onauthenticated.mock.calls.length).toBe(1);
  expect(storage).not.toHaveBeenCalled();
});

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

test('awaits a fresh snapshot when a mutation completes during an older refresh', async () => {
  const { fetch: backend, state } = mockServer();
  const mutation = deferred();
  const oldRefresh = deferred();
  const newRefresh = deferred();
  let refreshRequests = 0;
  let mutationStarted = false;
  let refreshing = false;
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof globalThis.fetch>(async (input, init) => {
      if (init?.method === 'PATCH') {
        mutationStarted = true;
        await mutation.promise;
      }
      // Capture the response before waiting so the first refresh contains old data.
      const response = await backend(input, init);
      if (refreshing && init?.method === 'GET') {
        refreshRequests++;
        await (refreshRequests <= 3 ? oldRefresh.promise : newRefresh.promise);
      }
      return response;
    })
  );
  await render(ManagementApp);
  await loaded();
  await navigate('Credentials');
  await page.getByRole('switch', { name: `Enable ${longName}`, exact: true }).click();
  await expect.poll(() => mutationStarted).toBe(true);
  refreshing = true;
  await page.getByRole('button', { name: 'Refresh data' }).click();
  await expect.poll(() => refreshRequests).toBe(3);
  mutation.resolve();
  await expect.poll(() => state.files[0].disabled).toBe(true);
  await expect
    .element(page.getByText('Credential disabled.', { exact: true }))
    .not.toBeInTheDocument();
  oldRefresh.resolve();
  await expect.poll(() => refreshRequests).toBe(6);
  await expect
    .element(page.getByText('Credential disabled.', { exact: true }))
    .not.toBeInTheDocument();
  newRefresh.resolve();
  await expect.element(page.getByText('Credential disabled.', { exact: true })).toBeVisible();
  await expect
    .element(page.getByRole('switch', { name: `Enable ${longName}`, exact: true }))
    .not.toBeChecked();
  await expect.element(page.getByRole('button', { name: 'Refresh data' })).toBeEnabled();
});

test('a mutation 401 clears revealed credentials and shows the sign-in action immediately', async () => {
  const { state, fetch } = mockServer();
  state.keys.push('another-client-key');
  await render(ManagementApp);
  await loaded();
  await navigate('API keys');
  await page.getByRole('button', { name: 'Reveal key 1', exact: true }).click();
  expect(document.body.textContent).toContain(longKey);
  await page.getByRole('button', { name: 'Delete key 1', exact: true }).click();
  state.sessionExpired = true;
  await page.getByRole('button', { name: 'Delete key', exact: true }).click();
  await expect.element(page.getByRole('heading', { name: 'Session ended' })).toBeVisible();
  await expect
    .element(page.getByRole('link', { name: 'Sign in', exact: true }))
    .toHaveAttribute('href', '/login');
  expect(document.body.textContent).not.toContain(longKey);
  expect(document.body.textContent).not.toContain(longName);
  expect(document.body.textContent).not.toContain(secret);
  expect(
    fetch.mock.calls.filter(
      ([url, options]) => String(url).endsWith('/api-keys/mutate') && options?.method === 'POST'
    )
  ).toHaveLength(1);
});

test.each([
  { label: 'a single key', keys: [longKey] },
  { label: 'duplicate and blank entries', keys: [longKey, ` ${longKey} `, ''] }
])('protects the final effective API key with $label', async ({ keys }) => {
  const { state, fetch } = mockServer();
  state.keys = keys;
  await render(ManagementApp);
  await loaded();
  await navigate('API keys');
  await expect
    .element(page.getByRole('button', { name: 'Delete key 1', exact: true }))
    .toBeDisabled();
  if (keys.length > 1)
    await expect
      .element(page.getByRole('button', { name: 'Delete key 2', exact: true }))
      .toBeDisabled();
  await expect
    .element(page.getByText('Add another key before deleting the final key.', { exact: false }))
    .toBeVisible();
  expect(document.body.textContent).toMatch(/An empty key list disables API\s+authentication\./);
  expect(fetch.mock.calls.every(([, options]) => options?.method === 'GET')).toBe(true);
});

test('deletes the selected key by value without a GET preflight when another operator changes the list', async () => {
  const { state, fetch } = mockServer();
  state.keys = [longKey, 'selected-for-removal'];
  await render(ManagementApp);
  await loaded();
  await navigate('API keys');
  await page.getByRole('button', { name: 'Delete key 2', exact: true }).click();
  // The server list changes after this browser loaded its copy.
  state.keys.unshift('concurrently-added-key');
  fetch.mockClear();
  await page.getByRole('button', { name: 'Delete key', exact: true }).click();
  await expect.element(page.getByRole('status')).toHaveTextContent('API key deleted.');
  const [url, options] = fetch.mock.calls[0];
  expect(url).toBe('/api/management/api-keys/mutate');
  expect(options?.method).toBe('POST');
  expect(JSON.parse(String(options?.body))).toEqual({
    action: 'remove',
    value: 'selected-for-removal'
  });
  expect(state.keys).toEqual(['concurrently-added-key', longKey]);
  expect(
    fetch.mock.calls.some(
      ([requestURL, init]) =>
        String(requestURL) === '/api/management/api-keys' && init?.method === 'GET'
    )
  ).toBe(true);
});
