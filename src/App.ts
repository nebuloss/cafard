import type { Clock } from '@/core/Clock.ts';
import { SystemClock } from '@/core/Clock.ts';
import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import type { KeyValueStore } from '@/core/KeyValueStore.ts';
import { LocalStorageStore } from '@/core/KeyValueStore.ts';
import { PageRegistry } from '@/pages/PageRegistry.ts';

/** The services every page may receive. Built once, here. */
export interface Services {
  readonly clock: Clock;
  readonly storage: KeyValueStore;
}

/**
 * Composition root: the only place that constructs services. It picks the
 * page from <body data-page> and starts it. Pages are registered here, one
 * line each.
 */
export class App {
  private readonly bag = new DisposableBag();
  readonly services: Services;
  readonly pages: PageRegistry;

  constructor(
    private readonly root: Document,
    services?: Partial<Services>,
  ) {
    this.services = {
      clock: services?.clock ?? new SystemClock(),
      storage: services?.storage ?? new LocalStorageStore(),
    };
    this.pages = new PageRegistry();
  }

  start(): Disposable {
    this.root.documentElement.classList.add('has-script');
    const page = this.pages.create(this.root);
    if (page !== null) this.bag.add(page.start());
    return this.bag;
  }
}
