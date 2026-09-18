import { expect, test, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import ManagementApp from '$lib/components/ManagementApp.svelte';
import { demoNotifications } from '$lib/demo';

test.each([200, 502, 401])(
  'holds section navigation until a notification save finishes with %i',
  async (status) => {
    let saved = {
      ...demoNotifications,
      url: 'https://ntfy.example.com',
      topic: 'old-topic',
      status: { enabled: false, configured: true, in_flight: false }
    };
    let releaseSave!: () => void;
    const saveGate = new Promise<void>((resolve) => {
      releaseSave = resolve;
    });
    let saving = false;
    let reads = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const path = new URL(String(input), window.location.origin).pathname;
        if (path.endsWith('/notifications') && init?.method === 'PUT') {
          saving = true;
          const update = JSON.parse(String(init.body));
          await saveGate;
          if (status !== 200) return Response.json({ error: 'private-upstream-error' }, { status });
          saved = { ...saved, ...update };
          return Response.json(saved);
        }
        if (path.endsWith('/notifications')) {
          reads++;
          return Response.json(saved);
        }
        if (path.endsWith('/auth-files')) return Response.json({ files: [] });
        if (path.endsWith('/api-keys')) return Response.json({ 'api-keys': [] });
        if (path.endsWith('/config')) return Response.json({});
        throw new Error(`Unexpected request: ${path}`);
      })
    );
    render(ManagementApp);
    await expect.element(page.getByRole('button', { name: 'Refresh data' })).toBeEnabled();
    const nav = page.getByRole('navigation', { name: 'Management sections' });
    const overview = nav.getByRole('button', { name: 'Overview', exact: true });
    const settings = nav.getByRole('button', { name: 'Settings', exact: true });
    await settings.click();
    const topic = page.getByLabelText('Topic', { exact: true });
    await expect.element(topic).toHaveValue('old-topic');
    await topic.fill('new-topic');
    await page.getByRole('button', { name: 'Save notifications' }).click();
    await expect.poll(() => saving).toBe(true);
    try {
      await expect.element(overview).toBeDisabled();
      await expect.element(settings).toBeDisabled();
      // A native click on the disabled navigation must not unmount the pending save.
      document
        .querySelector<HTMLButtonElement>('nav[aria-label="Management sections"] button')!
        .click();
      await expect.element(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
      expect(reads).toBe(1);
    } finally {
      releaseSave();
    }
    if (status === 401) {
      await expect.element(page.getByRole('heading', { name: 'Session ended' })).toBeVisible();
      await expect.element(page.getByRole('link', { name: 'Sign in' })).toBeVisible();
      await expect.element(topic).not.toBeInTheDocument();
      expect(document.body.textContent).not.toContain('new-topic');
      return;
    }
    await expect.element(overview).toBeEnabled();
    if (status === 502) {
      await expect
        .element(page.getByRole('alert'))
        .toHaveTextContent('The server could not complete');
      await expect.element(topic).toHaveValue('new-topic');
      await expect.element(page.getByRole('button', { name: 'Save notifications' })).toBeEnabled();
      expect(saved.topic).toBe('old-topic');
      expect(document.body.textContent).not.toContain('private-upstream-error');
      return;
    }
    await expect
      .element(page.getByText('Notification settings saved.', { exact: true }))
      .toBeVisible();
    expect(saved.topic).toBe('new-topic');
    await overview.click();
    await settings.click();
    await expect.element(topic).toHaveValue('new-topic');
    await expect.element(page.getByRole('button', { name: 'Save notifications' })).toBeDisabled();
    expect(reads).toBe(2);
  }
);
