import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [tailwindcss(), sveltekit()],
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:8317', changeOrigin: false },
      '/auth': { target: 'http://127.0.0.1:8317', changeOrigin: false }
    }
  }
});
