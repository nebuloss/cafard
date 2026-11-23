// One Vite project, one build per UI: `vite build --mode admin` and, from
// step 4c, `--mode chat`. Each is self-contained under dist/<mode>, with its
// own assets, so the admin UI's code is never served on the public listener:
// the two listeners embed different directories (internal/webui).
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const here = fileURLToPath(new URL('.', import.meta.url));

// Where each UI's API answers during `npm run dev:<mode>`, with `cafard serve`
// running on the default loopback ports.
const devAPI: Record<string, Record<string, string>> = {
  admin: { '/admin/api': 'http://127.0.0.1:8331' },
  chat: { '/api': 'http://127.0.0.1:8330', '/events': 'http://127.0.0.1:8330' },
};

export default defineConfig(({ mode }) => ({
  root: resolve(here, 'src', mode),
  plugins: [tailwindcss()],
  base: '/',
  publicDir: false,
  resolve: { alias: { '@shared': resolve(here, 'src/shared') } },
  build: {
    outDir: resolve(here, 'dist', mode),
    emptyOutDir: true,
    // The pages' Content-Security-Policy allows scripts from 'self' only.
    // Vite's preload polyfill is a module of its own, not inline, but it is
    // not needed by any browser that runs the rest, so it is left out.
    modulePreload: { polyfill: false },
    // Nothing inlined as data: URIs: what is served is a file, and the
    // policy's img-src would otherwise have to allow data: for it.
    assetsInlineLimit: 0,
    sourcemap: false,
    reportCompressedSize: false,
  },
  server: { proxy: devAPI[mode] ?? {} },
}));
