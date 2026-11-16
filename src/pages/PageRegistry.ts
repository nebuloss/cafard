import type { Page, PageFactory } from '@/pages/Page.ts';

/** Maps a data-page value to the Page that enhances it. */
export class PageRegistry {
  private readonly factories = new Map<string, PageFactory>();

  register(name: string, factory: PageFactory): this {
    if (this.factories.has(name)) throw new Error(`page ${name} registered twice`);
    this.factories.set(name, factory);
    return this;
  }

  /** The page for this document, or null when nothing is registered for it. */
  create(root: Document): Page | null {
    const name = root.body?.dataset['page'];
    if (name === undefined || name === '') return null;
    return this.factories.get(name)?.(root) ?? null;
  }
}
