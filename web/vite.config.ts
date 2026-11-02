// SPDX-License-Identifier: AGPL-3.0-or-later
import { defineConfig, loadEnv, type ProxyOptions } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Default dev target: a local nasbusterd (self-signed TLS). Override with CWNSB_API,
// e.g. `CWNSB_API=https://192.168.1.50:8443 pnpm dev`, or in web/.env.local.
const DEFAULT_API = 'https://127.0.0.1:8443'

// Production CSP served by nasbusterd (no inline script/style, no external origin).
const CSP = "default-src 'self'; img-src 'self' data:"

function apiProxy(target: string): Record<string, ProxyOptions> {
  return {
    '/api': {
      target,
      secure: false, // nasbusterd uses a self-signed certificate in dev
      changeOrigin: false, // keep Host = the dev server, so Origin and Host stay consistent
      ws: false,
      configure(proxy) {
        // Server-Sent Events: ask upstream for an uncompressed stream and make sure
        // nothing between here and the browser buffers it.
        proxy.on('proxyReq', (proxyReq, req) => {
          if (req.headers.accept?.includes('text/event-stream')) {
            proxyReq.setHeader('accept-encoding', 'identity')
          }
        })
        proxy.on('proxyRes', (proxyRes) => {
          const type = proxyRes.headers['content-type'] ?? ''
          if (type.startsWith('text/event-stream')) {
            proxyRes.headers['cache-control'] = 'no-cache, no-transform'
            proxyRes.headers['x-accel-buffering'] = 'no'
            proxyRes.socket?.setNoDelay(true)
          }
        })
      },
    },
  }
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const target = env.CWNSB_API || DEFAULT_API
  const proxy = apiProxy(target)

  return {
    plugins: [react(), tailwindcss()],
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      sourcemap: false,
      target: 'es2022',
      // Never inline assets as data: URLs (fonts would violate the CSP font-src 'self').
      assetsInlineLimit: 0,
      modulePreload: { polyfill: false },
    },
    server: { proxy },
    // `pnpm preview` serves the production build under the same CSP as nasbusterd,
    // so a violation shows up before it ships.
    preview: { proxy, headers: { 'Content-Security-Policy': CSP } },
  }
})
