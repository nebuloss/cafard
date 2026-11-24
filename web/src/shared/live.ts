/**
 * A tab's one stream of notices, after claudication's (web/src/api/live.ts
 * there, docs/data-architecture.md): the server says what changed and
 * nothing of it, and the page reads again through the API.
 *
 * - Closed while the tab is hidden: a hidden tab is told nothing, costs the
 *   server nothing, and reads again once when it is shown, which is the one
 *   notice it needs. On a phone that is most of the time.
 * - A stream that dropped and came back reads again: what changed while it
 *   was down was told to nobody.
 * - A stream refused (a lapsed sign-in, a proxy answering 502) is not tried
 *   again by the browser: the page reads at once, so a lapsed sign-in shows
 *   the sign-in screen now, and the stream is tried again every RETRY_MS.
 * - While the stream is down, the page reads every FLOOR_MS, as a floor.
 * - Whether a new UI was deployed is checked when the stream comes back (a
 *   deploy ends every stream) and when the tab is shown.
 */

/** How long after the browser gave up on a stream it is opened again. */
const RETRY_MS = 5_000;

/** How often the page reads while the stream is down: a floor, not a rate. */
const FLOOR_MS = 30_000;

/** How long notices are gathered before the page is told, so a burst is one read. */
const GATHER_MS = 30;

export interface LiveHandlers {
  /** A named event with its data, as the server sent it. */
  event: (name: string, data: unknown) => void;
  /** Read everything again: notices may have been missed. */
  resync: () => void;
  /** A new UI was deployed since this page loaded. */
  updated?: () => void;
}

export class LiveStream {
  private source: EventSource | null = null;
  private retry: ReturnType<typeof setTimeout> | undefined;
  private floor: ReturnType<typeof setInterval> | undefined;
  private gather: ReturnType<typeof setTimeout> | undefined;
  private pending: { name: string; data: unknown }[] = [];
  private resyncDue = false;
  private wanted = false;
  private connected = false;
  private dropped = false;
  private told = false;
  private readonly watchers = new Set<() => void>();
  private readonly entry = LiveStream.entryScript(document);

  constructor(
    private readonly url: string,
    private readonly events: readonly string[],
    private readonly on: LiveHandlers,
  ) {
    document.addEventListener('visibilitychange', () => {
      if (!this.wanted) return;
      if (document.visibilityState === 'visible') {
        this.open();
        this.queueResync();
        void this.checkUpdate();
      } else {
        this.close();
      }
    });
  }

  /** Whether notices are arriving now. */
  get live(): boolean {
    return this.connected;
  }

  /** Has fn called whenever live changes; returns what stops it. */
  watch(fn: () => void): () => void {
    this.watchers.add(fn);
    return () => this.watchers.delete(fn);
  }

  start(): void {
    this.wanted = true;
    if (document.visibilityState === 'visible') this.open();
  }

  stop(): void {
    this.wanted = false;
    this.close();
  }

  private open(): void {
    if (this.source) return;
    clearTimeout(this.retry);
    const src = new EventSource(this.url, { withCredentials: true });
    this.source = src;
    src.addEventListener('ready', () => {
      if (this.dropped) {
        this.queueResync();
        void this.checkUpdate();
      }
      this.dropped = false;
      this.setConnected(true);
    });
    src.onerror = () => {
      this.dropped = true;
      this.setConnected(false);
      // The browser retries a dropped stream itself, but not one refused.
      if (src.readyState === EventSource.CLOSED && this.source === src) {
        this.source = null;
        this.queueResync();
        this.retry = setTimeout(() => {
          if (this.wanted && document.visibilityState === 'visible') this.open();
        }, RETRY_MS);
      }
    };
    for (const name of this.events) {
      src.addEventListener(name, (e) => {
        let data: unknown = null;
        try {
          data = JSON.parse((e as MessageEvent<string>).data);
        } catch {
          /* a malformed notice is read as "something changed" */
        }
        this.pending.push({ name, data });
        this.tell();
      });
    }
  }

  private close(): void {
    clearTimeout(this.retry);
    this.source?.close();
    this.source = null;
    this.setConnected(false);
  }

  private setConnected(on: boolean): void {
    // While down and visible, read every FLOOR_MS.
    clearInterval(this.floor);
    if (!on && this.wanted) {
      this.floor = setInterval(() => {
        if (document.visibilityState === 'visible') this.queueResync();
      }, FLOOR_MS);
    }
    if (on === this.connected) return;
    this.connected = on;
    for (const fn of this.watchers) fn();
  }

  private queueResync(): void {
    this.resyncDue = true;
    this.tell();
  }

  /** Passes on what gathered: one resync swallows every notice with it. */
  private tell(): void {
    if (this.gather !== undefined) return;
    this.gather = setTimeout(() => {
      this.gather = undefined;
      const resync = this.resyncDue;
      const pending = this.pending;
      this.resyncDue = false;
      this.pending = [];
      if (resync) this.on.resync();
      else for (const p of pending) this.on.event(p.name, p.data);
    }, GATHER_MS);
  }

  /** Asks the server for the page and compares its script with the one running. */
  private async checkUpdate(): Promise<void> {
    if (!this.on.updated || this.told || this.entry === '') return;
    try {
      const res = await fetch(location.pathname, { cache: 'no-cache', credentials: 'same-origin' });
      if (!res.ok) return;
      const served = LiveStream.entryScript(new DOMParser().parseFromString(await res.text(), 'text/html'));
      if (served !== '' && served !== this.entry) {
        this.told = true;
        this.on.updated();
      }
    } catch {
      /* offline, or the server mid-restart: the next check will tell */
    }
  }

  /** The page's module script, whose name changes with every build. */
  static entryScript(doc: Document): string {
    return doc.querySelector<HTMLScriptElement>('script[type="module"][src]')?.getAttribute('src') ?? '';
  }
}

/**
 * Runs a read, never two at once: a notice that arrives during one runs it
 * once more when it ends, since what it read may be older than the change;
 * any number of notices meanwhile is that one more.
 */
export class Serial {
  private busy = false;
  private again = false;

  constructor(private readonly fn: () => Promise<void>) {}

  async run(): Promise<void> {
    if (this.busy) {
      this.again = true;
      return;
    }
    this.busy = true;
    try {
      do {
        this.again = false;
        await this.fn();
      } while (this.again);
    } finally {
      this.busy = false;
    }
  }
}
