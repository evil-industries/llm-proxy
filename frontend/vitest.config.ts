import { fileURLToPath } from 'node:url';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { defineConfig } from 'vitest/config';
import { playwright } from '@vitest/browser-playwright';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

const require = createRequire(import.meta.url);
const axeSource = readFileSync(require.resolve('axe-core/axe.min.js'), 'utf8');

export default defineConfig({
  test: {
    projects: [
      {
        test: {
          name: 'unit',
          environment: 'node',
          include: ['src/**/*.test.ts'],
          exclude: ['src/**/*.browser.test.ts']
        }
      },
      {
        plugins: [tailwindcss(), svelte()],
        resolve: {
          alias: { $lib: fileURLToPath(new URL('./src/lib', import.meta.url)) },
          conditions: ['browser']
        },
        test: {
          name: 'browser',
          include: ['src/**/*.browser.test.ts'],
          setupFiles: ['./src/tests/setup.ts'],
          testTimeout: 30000,
          browser: {
            enabled: true,
            headless: true,
            provider: playwright(),
            instances: [{ browser: 'chromium' }],
            screenshotFailures: true,
            screenshotDirectory: './test-results/screenshots',
            commands: {
              async auditAccessibility(context) {
                const frame = await context.frame();
                await frame.evaluate(axeSource);
                return frame.evaluate(async () => {
                  const axe = (window as unknown as { axe: typeof import('axe-core') }).axe;
                  const result = await axe.run(document.querySelector('.app-shell')!, {
                    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] }
                  });
                  return result.violations.map(({ id, impact, nodes }) => ({
                    id,
                    impact,
                    targets: nodes.map((node) => node.target)
                  }));
                });
              }
            }
          }
        }
      }
    ]
  }
});
