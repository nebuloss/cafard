import { Dom } from '@/core/Dom.ts';
import { Observable } from '@/core/Observable.ts';
import type { Entry, Page, Seen } from '@/model/types.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { imagesOf } from '@/render/renderers/MessageRenderer.ts';
import { seenText } from '@/render/format.ts';
import { Component } from '@/components/Component.ts';
import { popupMenu } from '@/components/PopupMenu.ts';
import type { ElementScroller } from '@/components/ElementScroller.ts';
import type { ReadTracker } from '@/components/ReadTracker.ts';
import type { ViewerImage } from '@/components/ImageViewer.ts';

/** What the conversation asks of the page around it. */
export interface ConversationHooks {
  reply(seq: number, quote: string): void;
  viewImages(images: readonly ViewerImage[], index: number): void;
  copy(text: string): void;
  /** Opens a log attachment's range in the logs pane; false lets the link go to Live. */
  openLog(href: string): boolean;
  /** Opens an event row in the logs pane; false lets the link go to Live. */
  openEvent(seq: number): boolean;
  /** Loads rows before seq (the "Older" page). */
  older(beforeSeq: number): Promise<Page<Entry>>;
}

/** Which rows a chip filter keeps, like messaging.keepRow. */
export function keeps(filter: string, viewer: string, e: Entry): boolean {
  switch (filter) {
    case 'questions':
      return e.question !== undefined;
    case 'events':
      return e.event === true;
    case 'mine':
      return e.kind === 'message' && e.sender?.name === viewer;
    case 'images':
      return imagesOf(e).length > 0;
    default:
      return true;
  }
}

export type NewState = { readonly mode: 'following' } | { readonly mode: 'paused'; readonly count: number };

/**
 * The conversation (PLAN 9.6.3): an aria-live log of messages, questions
 * and event rows. It opens at the newest; new rows scroll it only when the
 * reader is at the bottom, else a "N new" pill counts them; scrolling to
 * the top loads older rows. Rows visible for a second are acked.
 */
export class ConversationView extends Component {
  readonly newRows = new Observable<NewState>({ mode: 'following' }, (a, b) => JSON.stringify(a) === JSON.stringify(b));
  private readonly shown = new Set<number>();
  private oldest = 0;
  private hasOlder: boolean;
  private loading = false;
  private seen = new Map<number, string[]>();

  constructor(
    root: HTMLElement,
    private readonly viewer: string,
    private readonly filter: string,
    private readonly renderer: EntryRenderer,
    private readonly scroller: ElementScroller,
    private readonly reads: ReadTracker | null,
    private readonly hooks: ConversationHooks,
    private readonly pill: HTMLButtonElement | null,
  ) {
    super(root);
    this.hasOlder = root.hasAttribute('data-has-older');
  }

  protected override build(): void {
    for (const row of this.rows()) {
      const seq = Number(row.dataset['seq']);
      this.shown.add(seq);
      if (this.oldest === 0 || seq < this.oldest) this.oldest = seq;
      const seen = Dom.find('[data-slot="seen-by"]', row)?.textContent ?? '';
      if (seen.startsWith('seen by ')) this.seen.set(seq, seen.slice(8).split(', '));
      this.watch(row);
    }
  }

  protected bind(): void {
    this.scroller.toEnd();
    this.bag.add(
      this.scroller.onScroll(() => {
        if (this.scroller.atEnd()) this.newRows.set({ mode: 'following' });
        else if (this.newRows.value.mode === 'following') this.newRows.set({ mode: 'paused', count: 0 });
        if (this.scroller.atStart() && this.hasOlder) void this.loadOlder();
      }),
    );
    if (this.pill !== null) {
      const pill = this.pill;
      this.bag.listen(pill, 'click', () => {
        this.scroller.toEnd();
        this.newRows.set({ mode: 'following' });
      });
      this.bag.add(
        this.newRows.subscribe(
          (s) => {
            pill.hidden = s.mode === 'following' || s.count === 0;
            Dom.setText(Dom.find('[data-slot="new-count"]', pill), s.mode === 'paused' ? `${s.count} new` : '');
          },
          { immediate: true },
        ),
      );
    }
    this.bag.listen(this.root, 'click', (event) => this.click(event as MouseEvent));
  }

  private rows(): HTMLElement[] {
    return Array.from(this.root.children).filter((el): el is HTMLElement => el instanceof HTMLElement && el.dataset['seq'] !== undefined);
  }

  private watch(row: HTMLElement): void {
    const mine = row.hasAttribute('data-mine');
    if (!mine && (row.classList.contains('message') || row.classList.contains('question'))) this.reads?.watch(row);
  }

  /** Adds a new row at the end, if the filter keeps it. */
  add(entry: Entry): HTMLElement | null {
    if (this.shown.has(entry.seq) || !keeps(this.filter, this.viewer, entry)) return null;
    const atEnd = this.scroller.atEnd();
    const row = this.renderer.render(entry);
    this.shown.add(entry.seq);
    Dom.find('[data-slot="empty"]', this.root.parentElement ?? this.root)?.remove();
    this.root.append(row);
    this.watch(row);
    const s = this.newRows.value;
    if (atEnd || entry.sender?.name === this.viewer) {
      this.scroller.toEnd();
      this.newRows.set({ mode: 'following' });
    } else {
      this.newRows.set({ mode: 'paused', count: (s.mode === 'paused' ? s.count : 0) + 1 });
    }
    return row;
  }

  /** Replaces the rows, after a reset. */
  replace(entries: readonly Entry[]): void {
    this.root.replaceChildren();
    this.shown.clear();
    this.oldest = 0;
    for (const e of entries) {
      if (this.oldest === 0) this.oldest = e.seq;
      this.add(e);
    }
    this.scroller.toEnd();
  }

  /** A receipt: someone acked a message shown here. */
  markSeen(s: Seen): void {
    const row = Dom.find(`[data-seq="${s.seq}"]`, this.root);
    if (row === null || row.dataset['senderKind'] === undefined) return;
    const sender = Dom.find('[data-slot="sender"]', row)?.textContent ?? '';
    if (s.by === sender) return;
    const by = this.seen.get(s.seq) ?? [];
    if (!by.includes(s.by)) by.push(s.by);
    this.seen.set(s.seq, by);
    Dom.setText(Dom.find('[data-slot="seen-by"]', row), seenText(by));
  }

  async loadOlder(): Promise<void> {
    if (this.loading || !this.hasOlder || this.oldest === 0) return;
    this.loading = true;
    try {
      const page = await this.hooks.older(this.oldest);
      const rows = page.items.filter((e) => !this.shown.has(e.seq)).map((e) => {
        this.shown.add(e.seq);
        return this.renderer.render(e);
      });
      this.scroller.keepPosition(() => this.root.prepend(...rows));
      rows.forEach((r) => this.watch(r));
      const first = page.items[0];
      if (first !== undefined) this.oldest = first.seq;
      this.hasOlder = page.has_more && page.items.length > 0;
      if (!this.hasOlder) Dom.find('[data-slot="older"]', this.root.parentElement ?? this.root)?.remove();
    } finally {
      this.loading = false;
    }
  }

  private click(event: MouseEvent): void {
    const target = event.target as Element | null;
    const action = target?.closest<HTMLElement>('[data-action]');
    const row = target?.closest<HTMLElement>('[data-seq]');
    const seq = Number(row?.dataset['seq']);
    if (action !== null && action !== undefined && row !== null && row !== undefined) {
      const text = Dom.find('[data-slot="body"]', row)?.textContent ?? '';
      const sender = Dom.find('[data-slot="sender"]', row)?.textContent ?? '';
      switch (action.dataset['action']) {
        case 'reply':
          this.hooks.reply(seq, `reply to ${sender}: ${text.split('\n')[0]?.slice(0, 80) ?? ''}`);
          return;
        case 'menu':
          this.menu(action, seq, text);
          return;
      }
    }
    const image = target?.closest<HTMLAnchorElement>('a.image');
    if (image !== null && image !== undefined && row !== null && row !== undefined) {
      const tiles = Dom.findAll<HTMLAnchorElement>('a.image', row);
      const images = tiles.map((a) => ({ src: a.getAttribute('href') ?? '', alt: 'Photo' })).filter((i) => i.src.startsWith('/blob/'));
      event.preventDefault();
      this.hooks.viewImages(images, Math.max(0, tiles.indexOf(image)));
      return;
    }
    const log = target?.closest<HTMLAnchorElement>('a.log-open');
    if (log !== null && log !== undefined && this.hooks.openLog(log.getAttribute('href') ?? '')) {
      event.preventDefault();
      return;
    }
    const open = target?.closest<HTMLAnchorElement>('a.event-open');
    if (open !== null && open !== undefined && this.hooks.openEvent(seq)) event.preventDefault();
  }

  /** The message menu: copy text, copy seq, open in Live. */
  private menu(anchor: HTMLElement, seq: number, text: string): void {
    popupMenu(this.root, anchor, 'msg-popup', [
      { label: 'Copy text', run: () => this.hooks.copy(text) },
      { label: 'Copy seq', run: () => this.hooks.copy(String(seq)) },
      { label: 'Open in Live', href: `/live?to_seq=${seq}` },
    ]);
  }
}
