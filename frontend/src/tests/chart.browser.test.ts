import { expect, test } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';
import RequestChart from '$lib/components/RequestChart.svelte';
import { demoFiles } from '$lib/demo';

test('request activity shows only server-provided intervals and an accessible data alternative', async () => {
  render(RequestChart, {
    files: [
      {
        name: 'account',
        recent_requests: [
          { time: '23:50-00:00', success: 12, failed: 2 },
          { time: '00:00-00:10', success: 8, failed: 0 }
        ]
      }
    ]
  });
  await expect.element(page.getByRole('heading', { name: 'Request activity' })).toBeVisible();
  await expect
    .poll(() => document.querySelectorAll('.request-chart svg').length)
    .toBeGreaterThan(0);
  await page.getByText('View request data', { exact: false }).click();
  await expect.element(page.getByRole('table')).toBeVisible();
  const rows = [...document.querySelectorAll('tbody tr')].map((row) =>
    [...row.querySelectorAll('th, td')].map((cell) => cell.textContent?.trim())
  );
  expect(rows).toEqual([
    ['23:50-00:00', '12', '2'],
    ['00:00-00:10', '8', '0']
  ]);
});

for (const width of [320, 768]) {
  for (const zoom of [1, 2]) {
    test(`chart and expanded data remain readable at ${width}px with ${zoom * 100}% text`, async () => {
      await page.viewport(width, 900);
      render(RequestChart, { files: demoFiles });
      await expect
        .poll(
          () => document.querySelector('.request-chart svg')?.getBoundingClientRect().width ?? 0
        )
        .toBeGreaterThan(0);
      if (zoom === 2) {
        const nodes = [
          ...document.querySelectorAll<HTMLElement>('.request-chart, .request-chart *')
        ].filter((node) => !(node instanceof SVGElement));
        const sizes = nodes.map((node) => parseFloat(getComputedStyle(node).fontSize));
        nodes.forEach((node, index) => (node.style.fontSize = `${sizes[index] * 2}px`));
      }
      await page.getByText('View request data', { exact: false }).click();
      expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(width + 1);
      for (const element of document.querySelectorAll<HTMLElement>(
        '.chart-legend, .chart-legend span, summary, th, td'
      )) {
        expect(element.scrollWidth, element.textContent ?? '').toBeLessThanOrEqual(
          element.clientWidth + 1
        );
        expect(element.scrollHeight, element.textContent ?? '').toBeLessThanOrEqual(
          element.clientHeight + 1
        );
      }
      expect(document.querySelectorAll('tbody tr')).toHaveLength(20);
    });
  }
}

test('missing history has a useful empty state without a fabricated chart', async () => {
  render(RequestChart, { files: [{ name: 'account', success: 1000 }] });
  await expect.element(page.getByRole('heading', { name: 'No request history yet' })).toBeVisible();
  expect(document.querySelector('[data-slot="chart"]')).toBeNull();
});

for (const count of [50_000, 9_000_000_000]) {
  test(`large request counts (${count}) fit the chart at 320px and 200% text`, async () => {
    await page.viewport(320, 900);
    render(RequestChart, {
      files: [
        {
          name: 'busy-account',
          recent_requests: [
            { time: '09:00-09:10', success: count, failed: 2000 },
            { time: '09:10-09:20', success: count / 2, failed: 0 }
          ]
        }
      ]
    });
    await expect
      .poll(() => document.querySelectorAll('.request-chart svg text').length)
      .toBeGreaterThan(0);
    const elements = [
      ...document.querySelectorAll<HTMLElement>('.request-chart, .request-chart *')
    ];
    const sizes = elements.map((element) => parseFloat(getComputedStyle(element).fontSize));
    elements.forEach((element, index) => {
      element.style.fontSize = `${sizes[index] * 2}px`;
    });
    await expect
      .poll(() => {
        const panel = document.querySelector('.request-chart')!.getBoundingClientRect();
        return [...document.querySelectorAll<SVGTextElement>('.request-chart svg text')]
          .filter((text) => text.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }))
          .filter((text) => {
            const bounds = text.getBoundingClientRect();
            return (
              bounds.left < Math.max(0, panel.left) - 1 ||
              bounds.right > Math.min(window.innerWidth, panel.right) + 1 ||
              bounds.top < panel.top - 1 ||
              bounds.bottom > panel.bottom + 1
            );
          })
          .map((text) => text.textContent);
      })
      .toEqual([]);
    await page.getByText('View request data', { exact: false }).click();
    expect(document.querySelector('tbody td')?.textContent).toBe(
      new Intl.NumberFormat('en-US').format(count)
    );
  });
}

test('DST fallback intervals retain separate bars and data rows', async () => {
  await page.viewport(768, 900);
  const earlier = Date.parse('2026-10-25T00:00:00Z') / 1000;
  render(RequestChart, {
    files: [
      {
        name: 'account',
        recent_requests: [
          { time: '02:00-02:10', timestamp: earlier, success: 12, failed: 2 },
          { time: '02:00-02:10', timestamp: earlier + 3600, success: 8, failed: 1 }
        ]
      }
    ]
  });
  await page.getByText('View request data', { exact: false }).click();
  const rows = [...document.querySelectorAll('tbody tr')].map((row) =>
    [...row.querySelectorAll('th, td')].map((cell) => cell.textContent?.trim())
  );
  expect(rows).toEqual([
    ['02:00-02:10', '12', '2'],
    ['02:00-02:10', '8', '1']
  ]);
  await expect.poll(() => document.querySelectorAll('.lc-bars-bar').length).toBe(4);
  await expect
    .poll(
      () =>
        new Set(
          [...document.querySelectorAll('.lc-bars-bar')].map((bar) =>
            Math.round(bar.getBoundingClientRect().left)
          )
        ).size
    )
    .toBe(2);
});

test('portaled tooltips retain exact values inside a narrow viewport with enlarged text', async () => {
  await page.viewport(320, 900);
  render(RequestChart, {
    files: [
      {
        name: 'busy-account',
        recent_requests: Array.from({ length: 20 }, (_, index) => ({
          time: `${String(index).padStart(2, '0')}:00-${String(index).padStart(2, '0')}:10`,
          success: 26_000_000,
          failed: 300_000
        }))
      }
    ]
  });
  await expect.poll(() => document.querySelectorAll('.lc-tooltip-rect').length).toBe(20);
  const elements = [...document.querySelectorAll<HTMLElement>('.request-chart, .request-chart *')];
  const sizes = elements.map((element) => parseFloat(getComputedStyle(element).fontSize));
  elements.forEach((element, index) => {
    element.style.fontSize = `${sizes[index] * 2}px`;
  });

  // The tooltip is created on hover outside the chart, so the chart-only zoom misses it.
  await page.elementLocator(document.querySelector('.lc-tooltip-rect')!).hover();
  await expect.poll(() => document.querySelector('.lc-tooltip-root')).not.toBeNull();
  const tooltip = document.querySelector<HTMLElement>('.lc-tooltip-root')!;
  expect(document.querySelector('.request-chart')!.contains(tooltip)).toBe(false);
  const tooltipElements = [tooltip, ...tooltip.querySelectorAll<HTMLElement>('*')];
  const tooltipSizes = tooltipElements.map((element) =>
    parseFloat(getComputedStyle(element).fontSize)
  );
  tooltipElements.forEach((element, index) => {
    element.style.fontSize = `${tooltipSizes[index] * 2}px`;
  });

  for (const index of [0, 10, 19]) {
    await page.elementLocator(document.querySelectorAll('.lc-tooltip-rect')[index]).hover();
    await expect
      .poll(() => {
        const bounds = tooltip.getBoundingClientRect();
        return (
          bounds.left >= 0 &&
          bounds.right <= window.innerWidth &&
          bounds.top >= 0 &&
          bounds.bottom <= window.innerHeight
        );
      })
      .toBe(true);
    expect(tooltip.textContent).toContain((26_000_000).toLocaleString());
    expect(tooltip.textContent).toContain((300_000).toLocaleString());
    for (const span of tooltip.querySelectorAll('span')) {
      const bounds = span.getBoundingClientRect();
      expect(bounds.left).toBeGreaterThanOrEqual(0);
      expect(bounds.right).toBeLessThanOrEqual(window.innerWidth);
      expect(span.scrollWidth).toBeLessThanOrEqual(span.clientWidth + 1);
    }
  }
});
