import type { Disposable } from '@/core/Disposable.ts';

/** One kind of page, picked by <body data-page>. start() attaches behaviour. */
export interface Page {
  start(): Disposable;
}

export type PageFactory = (root: Document) => Page;
