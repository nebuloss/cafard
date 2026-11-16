import { defineConfig } from 'vite';
import { resolve } from 'node:path';

// The bundle is built into internal/web/assets/static/app so that Go's embed
// picks it up and the binary stays self-contained. The filename is fixed
// because the layout template names it; the server revalidates it by ETag.
export default defineConfig({
  resolve: { alias: { '@': resolve(import.meta.dirname, './src') } },
  build: {
    outDir: resolve(import.meta.dirname, 'internal/web/assets/static/app'),
    // Not emptied: the directory holds the committed README.txt that embed
    // needs on a clean checkout.
    emptyOutDir: false,
    target: 'es2022',
    cssCodeSplit: false,
    lib: {
      entry: resolve(import.meta.dirname, 'src/main.ts'),
      formats: ['es'],
      fileName: () => 'main.js',
    },
  },
});
