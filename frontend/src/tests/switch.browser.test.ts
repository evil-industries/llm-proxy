import { expect, test, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import Switch from '$lib/components/ui/switch/switch.svelte';

for (const size of ['default', 'sm'] as const) {
  test(`${size} switch moves its thumb and changes its track when toggled`, async () => {
    render(Switch, { size, 'aria-label': 'Enable request logging' });
    const control = page.getByRole('switch', { name: 'Enable request logging' });
    await expect.element(control).toHaveAttribute('aria-checked', 'false');
    const track = document.querySelector<HTMLElement>('[data-slot="switch"]')!;
    const thumb = track.querySelector<HTMLElement>('[data-slot="switch-thumb"]')!;
    const initialLeft = thumb.getBoundingClientRect().left;
    const initialColor = getComputedStyle(track).backgroundColor;

    await control.click();
    await expect.element(control).toHaveAttribute('aria-checked', 'true');
    await expect.poll(() => thumb.getBoundingClientRect().left - initialLeft).toBeGreaterThan(5);
    await expect.poll(() => getComputedStyle(track).backgroundColor).not.toBe(initialColor);
    expect(getComputedStyle(track).backgroundColor).not.toBe('rgba(0, 0, 0, 0)');

    await control.click();
    await expect.element(control).toHaveAttribute('aria-checked', 'false');
    await expect
      .poll(() => Math.abs(thumb.getBoundingClientRect().left - initialLeft))
      .toBeLessThan(0.5);
    await expect.poll(() => getComputedStyle(track).backgroundColor).toBe(initialColor);
  });
}

for (const checked of [false, true]) {
  test(`disabled ${checked ? 'checked' : 'unchecked'} switch rejects interaction`, async () => {
    const onCheckedChange = vi.fn();
    render(Switch, {
      disabled: true,
      checked,
      onCheckedChange,
      'aria-label': 'Enable request logging'
    });
    const control = page.getByRole('switch', { name: 'Enable request logging' });
    await expect.element(control).toBeDisabled();
    const track = document.querySelector<HTMLButtonElement>('[data-slot="switch"]')!;
    const thumb = track.querySelector<HTMLElement>('[data-slot="switch-thumb"]')!;
    const initialLeft = thumb.getBoundingClientRect().left;
    const initialColor = getComputedStyle(track).backgroundColor;
    track.click();
    track.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
    await expect.element(control).toHaveAttribute('aria-checked', String(checked));
    expect(onCheckedChange).not.toHaveBeenCalled();
    expect(thumb.getBoundingClientRect().left).toBe(initialLeft);
    expect(getComputedStyle(track).backgroundColor).toBe(initialColor);
    expect(Number(getComputedStyle(track).opacity)).toBeLessThan(1);
  });
}
