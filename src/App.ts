import { Backoff } from '@/core/Backoff.ts';
import type { Clock } from '@/core/Clock.ts';
import { SystemClock } from '@/core/Clock.ts';
import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import type { KeyValueStore } from '@/core/KeyValueStore.ts';
import { LocalStorageStore } from '@/core/KeyValueStore.ts';
import { CafardApi } from '@/net/CafardApi.ts';
import { FetchHttpClient, type HttpClient } from '@/net/HttpClient.ts';
import { browserEventSource, type EventSourceFactory } from '@/net/LiveFeed.ts';
import { AgentsPage } from '@/pages/AgentsPage.ts';
import { LivePage } from '@/pages/LivePage.ts';
import { PageRegistry } from '@/pages/PageRegistry.ts';
import { PairPage } from '@/pages/PairPage.ts';
import { StreamsPage } from '@/pages/StreamsPage.ts';
import { ChatPage } from '@/pages/ChatPage.ts';
import { DropPage } from '@/pages/DropPage.ts';
import { GalleryPage } from '@/pages/GalleryPage.ts';
import { HomePage } from '@/pages/HomePage.ts';
import { KeepOriginal } from '@/media/KeepOriginal.ts';
import { ResizeToJpeg } from '@/media/ResizeToJpeg.ts';
import { jobRepository } from '@/upload/JobRepository.ts';
import { XhrUploadClient } from '@/upload/UploadClient.ts';
import { UploadQueue } from '@/upload/UploadQueue.ts';

/** The services every page may receive. Built once, here. */
export interface Services {
  readonly clock: Clock;
  readonly storage: KeyValueStore;
  readonly http: HttpClient;
  readonly api: CafardApi;
  readonly eventSources: EventSourceFactory;
  /** A fresh backoff for each feed: SSE reconnects from 1 s to 30 s. */
  readonly backoff: () => Backoff;
  /** The browser's queue of messages on their way (track D). */
  readonly uploads: UploadQueue;
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
    const http = services?.http ?? new FetchHttpClient();
    const clock = services?.clock ?? new SystemClock();
    const keep = new KeepOriginal();
    this.services = {
      clock,
      storage: services?.storage ?? new LocalStorageStore(),
      http,
      api: services?.api ?? new CafardApi(http),
      eventSources: services?.eventSources ?? browserEventSource,
      backoff: services?.backoff ?? (() => new Backoff({ baseMs: 1000, maxMs: 30000 })),
      uploads:
        services?.uploads ??
        new UploadQueue(new XhrUploadClient(), jobRepository(root.defaultView ?? undefined), { resize: new ResizeToJpeg(keep), keep }, new Backoff({ baseMs: 2000, maxMs: 60000 }), clock),
    };
    this.pages = new PageRegistry();
    this.pages.register('live', (doc) => new LivePage(doc, this.services));
    this.pages.register('streams', (doc) => new StreamsPage(doc, this.services));
    this.pages.register('agents', (doc) => new AgentsPage(doc, this.services));
    this.pages.register('pair', (doc) => new PairPage(doc, this.services));
    this.pages.register('project', (doc) => new PairPage(doc, this.services));
    this.pages.register('projects', (doc) => new PairPage(doc, this.services));
    this.pages.register('home', (doc) => new HomePage(doc, this.services));
    this.pages.register('chat', (doc) => new ChatPage(doc, this.services));
    this.pages.register('drop', (doc) => new DropPage(doc, this.services));
    this.pages.register('gallery', (doc) => new GalleryPage(doc, this.services));
    // Plain pages of links: the shell only.
    this.pages.register('galleries', (doc) => new PairPage(doc, this.services));
    this.pages.register('item', (doc) => new PairPage(doc, this.services));
  }

  start(): Disposable {
    this.root.documentElement.classList.add('has-script');
    const page = this.pages.create(this.root);
    if (page !== null) this.bag.add(page.start());
    return this.bag;
  }
}
