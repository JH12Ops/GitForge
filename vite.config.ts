import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// Vite config: the renderer is built to dist/ and served by the Wails 3 Go
// gateway (desktop); the same build runs as a plain web dev server for the
// mocked mode (VITE_USE_MOCK=true via `npm run dev:mock`).
//
// `npm run dev` also spawns the headless Go devgateway (scripts/dev.mjs) and,
// when GITFORGE_LOCAL_TEST_ADDR is exported, proxies /gitforge/* (API + SSE)
// to it — real Go-backed git data in the browser with full vite HMR.

const devGateway = process.env.GITFORGE_LOCAL_TEST_ADDR

export default defineConfig({
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
      '@shared': resolve(__dirname, 'shared'),
    },
  },
  plugins: [vue(), tailwindcss()],
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
    ...(devGateway
      ? {
          proxy: {
            '/gitforge': {
              target: `http://${devGateway}`,
              changeOrigin: true,
            },
          },
        }
      : {}),
  },
  envPrefix: ['VITE_'],
  build: {
    outDir: 'dist',
    // WKWebView floor (macOS 11 is the bundle minimum): conservative
    // ES2020/Safari syntax target; WebView2 would cope with modern syntax.
    target: 'safari13',
    minify: true,
    sourcemap: false,
    rollupOptions: {
      // @vueuse (pulled in via reka-ui) ships `/* #__PURE__ */` comments whose
      // position Rollup cannot interpret; it drops them safely, so treat that
      // as vendor noise and keep every other warning visible.
      onwarn(warning, warn) {
        if (warning.code === 'INVALID_ANNOTATION' && warning.message.includes('@vueuse')) return
        warn(warning)
      },
      output: {
        // Split the vendor code out of the app chunk: the single 988 kB
        // index chunk tripped the 500 kB size warning, and the heavy
        // third-party deps change far less often than the app source.
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (id.includes('highlight.js')) return 'hljs'
          if (/[\\/](vue|pinia|@vueuse|reka-ui|motion-v|@lucide|@tanstack)[\\/]/.test(id)) return 'framework'
          return 'vendor'
        },
      },
    },
  },
})
