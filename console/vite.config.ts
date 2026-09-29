import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
//
// base: '/' — the console SPA mounts at the site root (osiris-gw reserves
// exactly '/' for the console slot, see docs/design/41 §0 and §10).
// build.outDir: 'dist' — this is the directory nginx (osiris-gw) is expected
// to serve as static files. Wiring gw/conf.d/default.conf to point here is
// out of scope for this worker (file ownership boundary) — noted for the
// supervisor.
export default defineConfig({
  base: '/',
  plugins: [react()],
  build: {
    outDir: 'dist',
  },
})
