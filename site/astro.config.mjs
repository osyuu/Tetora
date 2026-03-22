import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';

export default defineConfig({
  site: 'https://tetora.dev',
  output: 'static',
  integrations: [sitemap()],
  build: { format: 'directory' },
  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'zh-TW', 'ja', 'ko', 'es', 'fr', 'de', 'th', 'id', 'fil'],
    routing: { prefixDefaultLocale: false },
  },
});
