import SvelteRenderer from '@gardenjs/render-plugin-svelte';

export default {
  project_title: 'Components',
  serverport: typeof process !== 'undefined' ? Number(process.env.GARDEN_PORT || 3010) : 3010,
  no_open_browser: true,
  docs_link: true,
  themes: [{ name: 'Light', stageBg: '#fafafa', color: '#171717', active: true }],
  vite_config: './garden.vite.config.js',
  structure: {
    components: './src/lib/components',
    routes: './src/routes'
  },
  watch: {
    directories: ['./src'],
    include: ['.svelte', '.css', '.js', '.ts']
  },
  renderer: { svelte: SvelteRenderer },
  additional_style_files: ['src/app.css'],
  devices: {
    small: [
      { name: 'Small phone', w: 320, h: 640 },
      { name: 'Phone', w: 390, h: 844 }
    ],
    medium: [{ name: 'Tablet', w: 768, h: 1024 }],
    large: [
      { name: 'Laptop', w: 1280, h: 800 },
      { name: 'Desktop', w: 1440, h: 900 }
    ]
  }
};
