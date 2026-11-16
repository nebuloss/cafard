import type { Clock } from '@/core/Clock.ts';
import type { Disposable } from '@/core/Disposable.ts';

/** The part of IntersectionObserver the tracker uses. */
export interface ObserverLike {
  observe(el: Element): void;
  unobserve(el: Element): void;
  disconnect(): void;
}

export type ObserverFactory = (callback: (entries: readonly { target: Element; isIntersecting: boolean }[]) => void) => ObserverLike;

export const browserObserver: ObserverFactory = (callback) => new IntersectionObserver((entries) => callback(entries), { threshold: 0.6 });

/**
 * Acks a message once it has been visible for a second (PLAN 9.6.3, read
 * state). Seqs are gathered for a moment and acked in one request.
 */
export class ReadTracker implements Disposable {
  static readonly visibleMs = 1000;
  private static readonly batchMs = 400;
  private readonly observer: ObserverLike;
  private readonly timers = new Map<Element, Disposable>();
  private readonly done = new Set<number>();
  private pending: number[] = [];
  private flushTimer: Disposable | null = null;

  constructor(
    factory: ObserverFactory,
    private readonly clock: Clock,
    private readonly ack: (seqs: number[]) => Promise<unknown>,
  ) {
    this.observer = factory((entries) => {
      for (const e of entries) {
        if (e.isIntersecting) this.visible(e.target);
        else this.hidden(e.target);
      }
    });
  }

  /** Watches a message row; data-seq says which. */
  watch(el: HTMLElement): void {
    const seq = Number(el.dataset['seq']);
    if (!Number.isFinite(seq) || this.done.has(seq)) return;
    this.observer.observe(el);
  }

  private visible(el: Element): void {
    if (this.timers.has(el)) return;
    this.timers.set(
      el,
      this.clock.setTimeout(() => {
        this.timers.delete(el);
        this.observer.unobserve(el);
        const seq = Number((el as HTMLElement).dataset['seq']);
        if (this.done.has(seq)) return;
        this.done.add(seq);
        this.pending.push(seq);
        this.flushTimer ??= this.clock.setTimeout(() => this.flush(), ReadTracker.batchMs);
      }, ReadTracker.visibleMs),
    );
  }

  private hidden(el: Element): void {
    this.timers.get(el)?.dispose();
    this.timers.delete(el);
  }

  private flush(): void {
    this.flushTimer = null;
    const seqs = this.pending;
    this.pending = [];
    if (seqs.length > 0) {
      this.ack(seqs).catch(() => {
        // Not acked now; the row is read again on the next visit.
        for (const s of seqs) this.done.delete(s);
      });
    }
  }

  dispose(): void {
    for (const t of this.timers.values()) t.dispose();
    this.timers.clear();
    this.flushTimer?.dispose();
    this.flush();
    this.observer.disconnect();
  }
}
