import { Dom } from '@/core/Dom.ts';
import { disposable, type Disposable } from '@/core/Disposable.ts';
import { AnimationFrames, type FrameScheduler } from '@/core/FrameScheduler.ts';
import { Observable } from '@/core/Observable.ts';
import type { Entry } from '@/model/types.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { Component } from '@/components/Component.ts';
import { isTyping } from '@/components/FilterBar.ts';

/** Where the log scrolls: the window here, a pane in track D's chat. */
export interface Scroller {
  atEnd(): boolean;
  toEnd(): void;
  onScroll(listener: () => void): Disposable;
}

export class WindowScroller implements Scroller {
  constructor(private readonly win: Window) {}

  atEnd(): boolean {
    const doc = this.win.document.documentElement;
    return this.win.innerHeight + this.win.scrollY >= doc.scrollHeight - 48;
  }

  toEnd(): void {
    this.win.scrollTo({ top: this.win.document.documentElement.scrollHeight });
  }

  onScroll(listener: () => void): Disposable {
    this.win.addEventListener('scroll', listener, { passive: true });
    return disposable(() => this.win.removeEventListener('scroll', listener));
  }
}

export type FollowState = { readonly mode: 'following' } | { readonly mode: 'paused'; readonly newCount: number };

const sameFollow = (a: FollowState, b: FollowState): boolean =>
  a.mode === b.mode && (a.mode === 'following' || (b.mode === 'paused' && a.newCount === b.newCount));

/**
 * A bounded list of log rows (PLAN 9.6.4) that follows the end until the
 * reader scrolls up. At most cap rows, so a fast source cannot freeze the
 * tab; while paused, new entries wait outside the DOM so the rows read do
 * not move.
 */
export class LogView extends Component {
  static readonly cap = 5000;
  readonly follow = new Observable<FollowState>({ mode: 'following' }, sameFollow);
  private list!: HTMLElement;
  private lastSeq = 0;
  private scrolling = false;
  private queued: Entry[] = [];
  private held: Entry[] = [];
  private frame: Disposable | null = null;
  private skipped = 0; // queued entries dropped before a frame, still counted as new
  /** DOM batches applied, for tests and the curious. */
  flushes = 0;

  constructor(
    root: HTMLElement,
    private readonly renderer: EntryRenderer,
    private readonly scroller: Scroller,
    private readonly doc: Document,
    private readonly cap = LogView.cap,
    private readonly frames: FrameScheduler = new AnimationFrames(doc.defaultView),
  ) {
    super(root);
  }

  protected override build(): void {
    this.list = Dom.find('[data-slot="list"]', this.root) ?? this.root;
    const last = this.list.lastElementChild;
    if (last !== null) this.lastSeq = Dom.number(last, 'data-seq') ?? 0;
  }

  protected bind(): void {
    this.bag.defer(() => this.frame?.dispose());
    this.bag.add(
      this.scroller.onScroll(() => {
        if (this.scrolling) {
          this.scrolling = false;
          return;
        }
        const atEnd = this.scroller.atEnd();
        const f = this.follow.value;
        if (atEnd && f.mode === 'paused') this.resume();
        else if (!atEnd && f.mode === 'following') this.follow.set({ mode: 'paused', newCount: 0 });
      }),
    );
    const newButton = Dom.find<HTMLButtonElement>('[data-slot="new"]', this.root);
    const newCount = Dom.find('[data-slot="new-count"]', this.root);
    const toggle = Dom.find<HTMLButtonElement>('[data-slot="follow"]', this.root);
    if (newButton !== null) this.bag.listen(newButton, 'click', () => this.resume());
    if (toggle !== null) this.bag.listen(toggle, 'click', () => this.toggle());
    this.bag.listen(this.doc, 'keydown', (event) => {
      const e = event as KeyboardEvent;
      if (e.key === 'f' && !e.ctrlKey && !e.metaKey && !e.altKey && !isTyping(e.target)) this.toggle();
    });
    this.bag.add(
      this.follow.subscribe(
        (f) => {
          if (newButton !== null) newButton.hidden = f.mode === 'following' || f.newCount === 0;
          if (f.mode === 'paused') Dom.setText(newCount, `${f.newCount} new`);
          if (toggle !== null) {
            const following = f.mode === 'following';
            toggle.setAttribute('aria-pressed', String(following));
            toggle.setAttribute('aria-label', following ? 'Pause following' : 'Follow new lines');
            toggle.querySelector('use')?.setAttribute('href', `/static/icons.svg#${following ? 'pause' : 'play_arrow'}`);
          }
        },
        { immediate: true },
      ),
    );
  }

  /** The highest seq shown. */
  get cursor(): number {
    return this.lastSeq;
  }

  get size(): number {
    return this.list.children.length;
  }

  /** Entries waiting for the next frame or for the reader to resume. */
  get waiting(): number {
    return this.queued.length + this.held.length;
  }

  /**
   * Queues entries from a live feed; they reach the DOM on the next
   * animation frame, together with everything else that arrived by then.
   */
  enqueue(entries: readonly Entry[]): void {
    if (entries.length === 0) return;
    const all = this.queued.concat(entries);
    this.skipped += Math.max(0, all.length - this.cap);
    this.queued = keepLast(all, this.cap);
    this.frame ??= this.frames.request(() => {
      this.frame = null;
      const batch = this.queued;
      const skipped = this.skipped;
      this.queued = [];
      this.skipped = 0;
      this.append(batch, skipped);
    });
  }

  /**
   * Adds entries at the end, in seq order; ones already shown are skipped.
   * uncounted is how many newer entries were dropped before they got
   * here, which a paused view still counts as new.
   */
  append(entries: readonly Entry[], uncounted = 0): void {
    const fresh = entries.filter((e) => e.seq > Math.max(this.lastSeq, this.held[this.held.length - 1]?.seq ?? 0));
    if (fresh.length === 0) return;
    const f = this.follow.value;
    if (f.mode === 'paused') {
      this.held = keepLast(this.held.concat(fresh), this.cap);
      this.follow.set({ mode: 'paused', newCount: f.newCount + fresh.length + uncounted });
      return;
    }
    this.render(fresh);
    this.toEnd();
  }

  /** Replaces everything (a reset, a context view), whatever the mode. */
  replace(entries: readonly Entry[]): void {
    this.list.replaceChildren();
    this.lastSeq = 0;
    this.held = this.held.filter((e) => e.seq > (entries[entries.length - 1]?.seq ?? 0));
    this.render(entries);
    if (this.follow.value.mode === 'following') this.toEnd();
  }

  resume(): void {
    const held = this.held;
    this.held = [];
    this.follow.set({ mode: 'following' });
    this.render(held);
    this.toEnd();
  }

  toggle(): void {
    if (this.follow.value.mode === 'following') this.follow.set({ mode: 'paused', newCount: 0 });
    else this.resume();
  }

  /** Renders a run in one fragment and trims the oldest rows past cap. */
  private render(entries: readonly Entry[]): void {
    const fresh = keepLast(entries.filter((e) => e.seq > this.lastSeq), this.cap);
    if (fresh.length === 0) return;
    if (fresh.length >= this.cap) this.list.replaceChildren();
    const fragment = this.doc.createDocumentFragment();
    for (const entry of fresh) fragment.append(this.renderer.render(entry));
    this.list.append(fragment);
    this.lastSeq = fresh[fresh.length - 1]?.seq ?? this.lastSeq;
    for (let excess = this.list.children.length - this.cap; excess > 0; excess--) this.list.firstElementChild?.remove();
    this.flushes++;
  }

  private toEnd(): void {
    if (this.scroller.atEnd()) return;
    this.scrolling = true;
    this.scroller.toEnd();
  }
}

function keepLast<T>(items: T[], n: number): T[] {
  return items.length > n ? items.slice(items.length - n) : items;
}
