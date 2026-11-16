import { readdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { Browser } from 'happy-dom';

const pagesDir = resolve(import.meta.dirname, '../../internal/web/testdata/pages');
const apiDir = resolve(import.meta.dirname, '../../internal/web/testdata/api');

/**
 * Loads a page written by the Go template test into the test document:
 * body, data-page and every <template>. The file is parsed by a separate
 * happy-dom page (its content setter), then imported node by node, so no
 * HTML string is ever handed to the test document.
 */
export function loadPage(name: string, into: Document = document): void {
  const html = readFileSync(resolve(pagesDir, `${name}.html`), 'utf8');
  const browser = new Browser();
  try {
    const page = browser.newPage();
    page.content = html;
    const source = page.mainFrame.document as unknown as Document;
    const body = into.importNode(source.body, true) as HTMLElement;
    into.body.replaceWith(body);
    into.title = source.title;
  } finally {
    void browser.close();
  }
}

/** A JSON fixture written by the Go DTO test. */
export function fixture(name: string): unknown {
  return JSON.parse(readFileSync(resolve(apiDir, `${name}.json`), 'utf8'));
}

export function fixtureNames(): string[] {
  return readdirSync(apiDir)
    .filter((f) => f.endsWith('.json'))
    .map((f) => f.slice(0, -5));
}

export function readTestdata(path: string): string {
  return readFileSync(resolve(import.meta.dirname, '../../internal/web/testdata', path), 'utf8');
}
