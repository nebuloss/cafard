// The tests are of the classes that hold logic without a page: formatting,
// the picker's state, escaping. They run under node; nothing here needs a
// DOM, and what does is checked in a browser against a deployed box.
import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const here = fileURLToPath(new URL('.', import.meta.url));

export default defineConfig({
  resolve: { alias: { '@shared': resolve(here, 'src/shared') } },
  test: { include: ['test/**/*.test.ts'], environment: 'node' },
});
