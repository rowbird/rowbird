import { writeFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig, type Plugin } from 'vite'

const backend = process.env.ROWBIRD_DEV_BACKEND ?? 'http://localhost:8080'

// The Go binary embeds dist/, which must exist before the first web build. Vite empties the
// directory on every build, so the tracked placeholder is written back afterwards.
function keepDistPlaceholder(): Plugin {
  return {
    name: 'rowbird-keep-dist-placeholder',
    apply: 'build',
    closeBundle() {
      writeFileSync(fileURLToPath(new URL('./dist/.gitkeep', import.meta.url)), '')
    },
  }
}

export default defineConfig({
  plugins: [vue(), tailwindcss(), keepDistPlaceholder()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/api': backend,
      '/health': backend,
      // Shared links and metrics are served by the backend, not by the app. A regular expression
      // keeps "/r/" from also catching "/reports" and "/runs".
      '^/r/': backend,
      '/metrics': backend,
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
