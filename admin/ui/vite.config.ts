import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// Dev: `npm run dev` proxies /api to the Go server on ADMIN_ADDR (":8081" = localhost:8081). Run
// that server with ADMIN_ORIGIN=http://localhost:5173 so writes pass its Origin check.
const addr = process.env.ADMIN_ADDR || ':8081'
const target = addr.includes('://') ? addr : `http://${addr.startsWith(':') ? `localhost${addr}` : addr}`

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: { port: 5173, strictPort: true, proxy: { '/api': { target } } },
  build: { chunkSizeWarningLimit: 1024 }, // one bundle, embedded in the Go binary and served from it
})
