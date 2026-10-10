import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

const outDir = path.resolve(import.meta.dirname, '../internal/webui/dist')

// The Go embed directive needs at least one file in dist, so the placeholder
// removed by emptyOutDir is written back after every build.
function keepGitkeep(): Plugin {
  return {
    name: 'keep-gitkeep',
    closeBundle() {
      mkdirSync(outDir, { recursive: true })
      writeFileSync(path.join(outDir, '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), keepGitkeep()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  build: {
    outDir,
    emptyOutDir: true,
    chunkSizeWarningLimit: 1000,
  },
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:3100', changeOrigin: false },
    },
  },
})
