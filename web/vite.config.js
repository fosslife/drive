import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build output is embedded into the binary by embed.go next door, so dist/
// stays inside this directory: go:embed cannot reach above its own package.
export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true },
  // `npm run dev` talks to a drive running the ordinary way. There is no
  // separate dev API: the interface is a client of the one API in production
  // and in development both.
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
