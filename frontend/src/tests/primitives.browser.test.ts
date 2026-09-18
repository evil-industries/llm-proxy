import { expect, test } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import TabsStory from '$lib/components/ui/tabs/Tabs.example.svelte';
import SeparatorStory from '$lib/components/ui/separator/Separator.example.svelte';

test('horizontal tabs place content below their list and visibly mark the selected tab', async () => {
  render(TabsStory);
  const list = page.getByRole('tablist');
  await expect.element(list).toBeVisible();
  const listElement = document.querySelector('[role="tablist"]')!;
  const panel = document.querySelector('[role="tabpanel"]')!;
  expect(panel.getBoundingClientRect().top).toBeGreaterThanOrEqual(
    listElement.getBoundingClientRect().bottom
  );
  const tab = document.querySelector('[role="tab"][aria-selected="true"]')!;
  expect(getComputedStyle(tab).backgroundColor).not.toBe('rgba(0, 0, 0, 0)');
  await page.getByRole('tab', { name: 'Requests', exact: true }).click();
  await expect
    .element(page.getByRole('tabpanel'))
    .toHaveTextContent('The latest request completed successfully.');
});

test('vertical tabs arrange their triggers in a column beside the panel', async () => {
  render(TabsStory, { orientation: 'vertical' });
  await expect.element(page.getByRole('tablist')).toBeVisible();
  const list = document.querySelector('[role="tablist"]')!;
  expect(getComputedStyle(list).flexDirection).toBe('column');
  expect(
    document.querySelector('[role="tabpanel"]')!.getBoundingClientRect().left
  ).toBeGreaterThanOrEqual(list.getBoundingClientRect().right);
});

test('the default separator has a visible horizontal track', async () => {
  render(SeparatorStory);
  await expect.element(page.getByText('Provider credentials', { exact: true })).toBeVisible();
  const separator = document.querySelector('[data-slot="separator"]')!;
  expect(separator.getBoundingClientRect().height).toBeGreaterThanOrEqual(1);
  expect(separator.getBoundingClientRect().width).toBeGreaterThan(10);
});
