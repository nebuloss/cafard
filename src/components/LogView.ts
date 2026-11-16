import { Dom } from '@/core/Dom.ts';
import { disposable, type Disposable } from '@/core/Disposable.ts';
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
 * A bounded list of log rows (PLAN 9.6.4). It follows the end while the
 * reader is at the bottom; scrolling up pauses it, and a "N new" button
 * resumes. Past cap rows the oldest are dropped, so a fast source cannot
 * freeze the tab.
 */
export class LogView extends Component {
  static readonly cap = 5000;
  readonly follow = new Observable<FollowState>({ mode: 'following' }, sameFollow);
  private list!: HTMLElement;
  private lastSeq = 0;
  private scrolling = false;

  constructor(
    root: HTMLElement,
    private readonly renderer: EntryRenderer,
    private readonly scroller: Scroller,
    private readonly doc: Document,
    private readonly cap = LogView.cap,
  ) {
    super(root);
  }

  protected override build(): void {
    this.list = Dom.find('[data-slot="list"]', this.root) ?? this.root;
    const last = this.list.lastElementChild;
    if (last !== null) this.lastSeq = Dom.number(last, 'data-seq') ?? 0;
  }

  protected bind(): void {
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

  /** Adds entries at the end, in seq order; ones already shown are skipped. */
  append(entries: readonly Entry[]): void {
    let added = 0;
    for (const entry of entries) {
      if (entry.seq <= this.lastSeq) continue;
      this.list.append(this.renderer.render(entry));
      this.lastSeq = entry.seq;
      added++;
    }
    if (added === 0) return;
    while (this.list.children.length > this.cap) this.list.firstElementChild?.remove();
    const f = this.follow.value;
    if (f.mode === 'following') this.toEnd();
    else this.follow.set({ mode: 'paused', newCount: f.newCount + added });
  }

  /** Replaces everything, after a reset. */
  replace(entries: readonly Entry[]): void {
    this.list.replaceChildren();
    this.lastSeq = 0;
    this.append(entries);
  }

  resume(): void {
    this.follow.set({ mode: 'following' });
    this.toEnd();
  }

  toggle(): void {
    if (this.follow.value.mode === 'following') this.follow.set({ mode: 'paused', newCount: 0 });
    else this.resume();
  }

  private toEnd(): void {
    if (this.scroller.atEnd()) return;
    this.scrolling = true;
    this.scroller.toEnd();
  }
}
