import type { Backoff } from '@/core/Backoff.ts';
import type { Clock } from '@/core/Clock.ts';
import { DisposableBag, disposable, type Disposable } from '@/core/Disposable.ts';
import { EventBus } from '@/core/EventBus.ts';
import { Observable } from '@/core/Observable.ts';
import { parseAnswered, parseChat, parseCounts, parseEntry, parseReset, parseSeen, parseStream } from '@/model/parse.ts';
import type { Answered, Chat, Counts, Entry, Reset, Seen, Stream } from '@/model/types.ts';

/** The part of EventSource LiveFeed uses, so tests can fake it. */
export interface EventSourceLike {
  onopen: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
  addEventListener(type: string, listener: (event: MessageEvent) => void): void;
  close(): void;
}

export type EventSourceFactory = (url: string) => EventSourceLike;

export const browserEventSource: EventSourceFactory = (url) => new EventSource(url);

export type FeedState =
  | { readonly state: 'connecting' }
  | { readonly state: 'live' }
  | { readonly state: 'retrying'; readonly attempt: number; readonly inMs: number }
  | { readonly state: 'closed' };

/** What the feed emits (CONTRACTS.md 4). */
export interface FeedEvents {
  entry: Entry;
  counts: Counts;
  stream_status: Stream;
  /** The server asks for a reload through the JSON API, then restart(). */
  reset: Reset;
  seen: Seen;
  answered: Answered;
  chat: Chat;
}

const parsers: { readonly [K in keyof FeedEvents]: (raw: unknown) => FeedEvents[K] } = {
  entry: (raw) => parseEntry(raw),
  counts: parseCounts,
  stream_status: (raw) => parseStream(raw),
  reset: parseReset,
  seen: parseSeen,
  answered: parseAnswered,
  chat: (raw) => parseChat(raw),
};

/**
 * Adapter over EventSource for one page's /api/events connection
 * (CONTRACTS.md 4). The browser's own retry is replaced by ours: on an
 * error the source is closed and reopened after a backoff, with
 * since_seq set to one past the last entry seen, which is what
 * Last-Event-ID would carry (EventSource cannot set headers).
 */
export class LiveFeed implements Disposable {
  readonly state = new Observable<FeedState>({ state: 'closed' }, (a, b) => JSON.stringify(a) === JSON.stringify(b));
  readonly events = new EventBus<FeedEvents>();
  private source: EventSourceLike | null = null;
  private retry: Disposable | null = null;
  /** The next seq wanted, the resume point; 0 means "only new entries". */
  private next = 0;
  private readonly bag = new DisposableBag();

  /** path is "/api/events?..." without since_seq. */
  constructor(
    private readonly factory: EventSourceFactory,
    private readonly clock: Clock,
    private readonly backoff: Backoff,
    private readonly path: string,
  ) {}

  /** The next seq the feed waits for. */
  get cursor(): number {
    return this.next;
  }

  /** Opens the feed; entries from sinceSeq on are replayed first. */
  start(sinceSeq = 0): Disposable {
    this.next = Math.max(0, sinceSeq);
    this.open();
    return disposable(() => this.dispose());
  }

  /** Reconnects from sinceSeq, after a reset was handled. */
  restart(sinceSeq: number): void {
    this.closeSource();
    this.next = Math.max(0, sinceSeq);
    this.backoff.reset();
    this.open();
  }

  dispose(): void {
    this.closeSource();
    this.bag.dispose();
    this.state.set({ state: 'closed' });
  }

  private url(): string {
    const sep = this.path.includes('?') ? '&' : '?';
    return this.next > 0 ? `${this.path}${sep}since_seq=${this.next}` : this.path;
  }

  private open(): void {
    if (this.bag.isDisposed) return;
    this.state.set({ state: 'connecting' });
    const source = this.factory(this.url());
    this.source = source;
    source.onopen = () => {
      if (this.source !== source) return;
      this.backoff.reset();
      this.state.set({ state: 'live' });
    };
    source.onerror = () => {
      if (this.source !== source) return;
      this.scheduleRetry();
    };
    for (const name of Object.keys(parsers) as (keyof FeedEvents)[]) {
      source.addEventListener(name, (event) => {
        if (this.source === source) this.deliver(name, event);
      });
    }
  }

  private deliver<K extends keyof FeedEvents>(name: K, event: MessageEvent): void {
    let payload: FeedEvents[K];
    try {
      payload = parsers[name](JSON.parse(String(event.data))) as FeedEvents[K];
    } catch (error) {
      console.error('cafard: dropped a malformed event', name, error);
      return;
    }
    if (name === 'entry') {
      const seq = (payload as Entry).seq;
      if (seq < this.next) return;
      this.next = seq + 1;
    }
    if (name === 'reset') {
      // The server ends the stream after a reset; do not retry on our own.
      this.closeSource();
      this.state.set({ state: 'connecting' });
    }
    this.events.emit(name, payload);
  }

  private scheduleRetry(): void {
    this.closeSource();
    const inMs = this.backoff.next();
    this.state.set({ state: 'retrying', attempt: this.backoff.attempts, inMs });
    this.retry = this.clock.setTimeout(() => {
      this.retry = null;
      this.open();
    }, inMs);
  }

  private closeSource(): void {
    this.retry?.dispose();
    this.retry = null;
    if (this.source !== null) {
      this.source.onopen = null;
      this.source.onerror = null;
      this.source.close();
      this.source = null;
    }
  }
}
