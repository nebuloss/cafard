import type { Clock } from '@/core/Clock.ts';
import { Dom, h } from '@/core/Dom.ts';
import type { EventBus } from '@/core/EventBus.ts';
import { StorageKey, type KeyValueStore } from '@/core/KeyValueStore.ts';
import type { Counts, Stream } from '@/model/types.ts';
import type { FeedEvents } from '@/net/LiveFeed.ts';
import { badgeCount, messageTime } from '@/render/format.ts';
import { Component } from '@/components/Component.ts';

/** A sink's status from its streams', like pages.sinkStatus: down wins, then connected. */
export function sinkStatus(statuses: readonly string[]): string {
  if (statuses.includes('down')) return 'down';
  if (statuses.includes('connected')) return 'connected';
  return statuses[0] ?? '';
}

/** Each project's unread messages and open questions, summed over its chats, like pages.projectUnread. */
export function projectTotals(c: Counts): Map<string, { unread: number; questions: number }> {
  const out = new Map<string, { unread: number; questions: number }>();
  for (const chat of c.chats) {
    if (!chat.id.startsWith('chan:')) continue;
    const project = chat.id.slice(5).split('/', 1)[0] ?? '';
    const t = out.get(project) ?? { unread: 0, questions: 0 };
    out.set(project, { unread: t.unread + chat.unread, questions: t.questions + chat.open_questions });
  }
  return out;
}

const isFolds = (raw: unknown): Record<string, boolean> | null => {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null;
  const out: Record<string, boolean> = {};
  for (const [k, v] of Object.entries(raw)) if (typeof v === 'boolean') out[k] = v;
  return out;
};

/** The trees the reader unfolded or folded, by data-node ("dm", "p:lab"). */
export const SIDE_FOLDS = new StorageKey<Record<string, boolean>>('cafard.sideFolds', isFolds);

/**
 * The left bar (DECISIONS "a Discord-like left bar"): the direct messages
 * and each project as a <details> tree. It remembers which trees the
 * reader unfolded or folded (the page's own tree always opens, the way
 * the server renders it); keeps the badges of Home, the direct messages,
 * each project and Admin, the document title and the app badge from
 * counts; and each sink's dots and last line time from stream_status.
 * Below 840 px it is a drawer the top bar's link to #side opens (CSS
 * :target without the bundle; here a toggle that keeps the address
 * alone), closed by its close link, the scrim or Esc. The chat rows are a
 * ChatList of their own.
 */
export class SideNav extends Component {
  private readonly baseTitle: string;

  constructor(
    root: HTMLElement,
    private readonly doc: Document,
    private readonly events: EventBus<FeedEvents>,
    private readonly clock: Clock,
    private readonly storage: KeyValueStore,
  ) {
    super(root);
    this.baseTitle = doc.title.replace(/^\(\d+\) /, '');
  }

  protected bind(): void {
    for (const open of Dom.findAll('[data-action="open-side"]', this.doc)) {
      this.bag.listen(open, 'click', (event) => {
        event.preventDefault();
        this.open();
      });
    }
    for (const close of Dom.findAll('[data-action="close-side"]', this.doc)) {
      this.bag.listen(close, 'click', (event) => {
        event.preventDefault();
        this.close();
      });
    }
    this.bag.listen(this.doc, 'keydown', (event) => {
      if ((event as KeyboardEvent).key === 'Escape' && this.isOpen()) this.close();
    });
    this.restoreFolds();
    this.bag.add(this.events.on('counts', (c) => this.counts(c)));
    this.bag.add(this.events.on('stream_status', (s) => this.status(s)));
    // A page opened at #side (a link, or the no-script toggle) starts open.
    const win = this.doc.defaultView;
    if (win?.location.hash === '#side') {
      win.history.replaceState(win.history.state, '', `${win.location.pathname}${win.location.search}`);
      this.open();
    }
  }

  private trees(): HTMLDetailsElement[] {
    return Dom.findAll<HTMLDetailsElement>('details.tree[data-node]', this.root);
  }

  /** Unfolds and folds the trees as the reader left them, then records each toggle. */
  private restoreFolds(): void {
    const folds = this.storage.get(SIDE_FOLDS) ?? {};
    for (const tree of this.trees()) {
      const node = tree.dataset['node'] ?? '';
      const stored = folds[node];
      if (stored !== undefined && !tree.hasAttribute('data-current')) tree.open = stored;
      // The reader's own toggles only (a click, or Enter and Space, on the
      // head), not the toggle events of the page opening its tree: the
      // click comes before the details flips.
      const head = tree.querySelector<HTMLElement>(':scope > summary');
      if (head !== null) this.bag.listen(head, 'click', () => this.remember(node, !tree.open));
    }
  }

  private remember(node: string, open: boolean): void {
    const folds = this.storage.get(SIDE_FOLDS) ?? {};
    if (folds[node] === open) return;
    this.storage.set(SIDE_FOLDS, { ...folds, [node]: open });
  }

  isOpen(): boolean {
    return this.root.hasAttribute('data-open');
  }

  open(): void {
    this.root.setAttribute('data-open', '');
    for (const t of Dom.findAll('[data-action="open-side"]', this.doc)) t.setAttribute('aria-expanded', 'true');
    // The page's own row, so the list opens where the reader is.
    (Dom.find<HTMLElement>('.side-list [aria-current="page"]', this.root) ?? Dom.find<HTMLElement>('.side-list a[href]', this.root))?.focus({ preventScroll: true });
  }

  close(): void {
    if (!this.isOpen()) return;
    this.root.removeAttribute('data-open');
    const toggles = Dom.findAll<HTMLElement>('[data-action="open-side"]', this.doc);
    for (const t of toggles) t.setAttribute('aria-expanded', 'false');
    toggles[0]?.focus({ preventScroll: true });
  }

  /** The badges of the bar, the title and the app badge. */
  counts(c: Counts): void {
    const totals = projectTotals(c);
    for (const tree of Dom.findAll<HTMLElement>('details.tree-project[data-project]', this.root)) {
      const head = tree.querySelector<HTMLElement>(':scope > summary') ?? tree;
      const t = totals.get(tree.dataset['project'] ?? '');
      Dom.setText(Dom.find('[data-slot="unread"]', head), badgeCount(t?.unread ?? 0));
      Dom.setText(Dom.find('[data-slot="questions"]', head), badgeCount(t?.questions ?? 0));
    }
    for (const [name, n] of [['questions', c.open_questions], ['direct', c.unread_direct]] as const) {
      const badge = Dom.find(`[data-badge="${name}"]`, this.root);
      if (badge !== null) {
        badge.hidden = n === 0;
        Dom.setText(badge, badgeCount(n));
      }
    }
    const down = Dom.find('[data-badge="sources-down"]', this.root);
    if (down !== null) down.hidden = c.sources_down === 0;
    const chats = c.open_questions + c.unread_direct;
    this.doc.title = chats > 0 ? `(${chats}) ${this.baseTitle}` : this.baseTitle;
    const nav = this.doc.defaultView?.navigator as (Navigator & { setAppBadge?: (n: number) => Promise<void>; clearAppBadge?: () => Promise<void> }) | undefined;
    if (chats > 0) void nav?.setAppBadge?.(chats).catch(() => {});
    else void nav?.clearAppBadge?.().catch(() => {});
  }

  /** A stream of a listed sink changed: its dot, the sink's status and last line time. */
  status(s: Stream): void {
    if (s.project === undefined || s.sink === undefined) return;
    const row = Dom.findAll<HTMLElement>('[data-sink]', this.root).find((el) => el.dataset['sink'] === `${s.project}/${s.sink}`);
    if (row === undefined) return;
    const dots = Dom.find('.sink-dots', row);
    if (dots !== null) {
      const title = `${s.id}: ${s.status}`;
      const dot = Dom.findAll<HTMLElement>('.stream-dot', dots).find((d) => (d.getAttribute('title') ?? '').startsWith(`${s.id}:`));
      if (dot === undefined) dots.append(h('span', { class: 'stream-dot', 'data-status': s.status, title }));
      else {
        dot.dataset['status'] = s.status;
        dot.setAttribute('title', title);
      }
      row.dataset['status'] = sinkStatus(Dom.findAll<HTMLElement>('.stream-dot', dots).map((d) => d.dataset['status'] ?? ''));
    }
    const time = Dom.find('[data-slot="time"]', row);
    if (time !== null && s.last_activity !== undefined) {
      Dom.setText(time, messageTime(s.last_activity, this.clock.now()));
      time.setAttribute('datetime', s.last_activity);
    }
  }
}
