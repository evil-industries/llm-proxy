import { fileURLToPath } from 'node:url';
import { svelte, vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

const rootPath = (path) => fileURLToPath(new URL(path, import.meta.url));

/** @returns {import('vite').Plugin} */
function gardenStartup() {
  return {
    name: 'garden-await-listen',
    configureServer(server) {
      // Garden restarts immediately after calling listen without awaiting it.
      // Vite 7 needs the bound port before it can safely recreate the server.
      const listen = server.listen.bind(server);
      const restart = server.restart.bind(server);
      /** @type {ReturnType<typeof server.listen> | undefined} */
      let listening;
      server.listen = (...args) => {
        listening = listen(...args);
        return listening;
      };
      server.restart = async (...args) => {
        await listening;
        return restart(...args);
      };
    }
  };
}

export default defineConfig({
  plugins: [
    gardenStartup(),
    tailwindcss(),
    svelte({ configFile: false, preprocess: vitePreprocess() })
  ],
  resolve: {
    alias: [
      { find: '$lib', replacement: rootPath('./src/lib') },
      {
        find: /^\$app\/(.*)/,
        replacement: `${rootPath('./node_modules/@gardenjs/render-plugin-svelte/src/sveltekit_mocks')}/$1`
      }
    ],
    dedupe: ['svelte']
  },
  optimizeDeps: { include: ['bits-ui', 'layerchart'] },
  server: {
    host: '127.0.0.1',
    port: Number(process.env.GARDEN_PORT || 3010),
    strictPort: true,
    fs: { allow: [rootPath('.')] }
  }
});
