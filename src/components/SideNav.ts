import type { Clock } from '@/core/Clock.ts';
import { Dom, h } from '@/core/Dom.ts';
import type { EventBus } from '@/core/EventBus.ts';
import type { Counts, Stream } from '@/model/types.ts';
import type { FeedEvents } from '@/net/LiveFeed.ts';
import { badgeCount, messageTime } from '@/render/format.ts';
import { Component } from '@/components/Component.ts';
import { projectTotals } from '@/components/ProjectCards.ts';

/** A sink's status from its streams', like pages.sinkStatus: down wins, then connected. */
export function sinkStatus(statuses: readonly string[]): string {
  if (statuses.includes('down')) return 'down';
  if (statuses.includes('connected')) return 'connected';
  return statuses[0] ?? '';
}

/**
 * The side list of the project view: a drawer on narrow screens, which
 * the top bar's link to #side opens (CSS :target without the bundle; here
 * a toggle that keeps the address alone), closed by its close link, the
 * scrim or Esc. It keeps the switcher's badges from counts and each
 * sink's dots and last line time from stream_status; the chat rows are a
 * ChatList of their own.
 */
export class SideNav extends Component {
  constructor(
    root: HTMLElement,
    private readonly doc: Document,
    private readonly events: EventBus<FeedEvents>,
    private readonly clock: Clock,
  ) {
    super(root);
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
    this.bag.add(this.events.on('counts', (c) => this.counts(c)));
    this.bag.add(this.events.on('stream_status', (s) => this.status(s)));
    // A page opened at #side (a link, or the no-script toggle) starts open.
    const win = this.doc.defaultView;
    if (win?.location.hash === '#side') {
      win.history.replaceState(win.history.state, '', `${win.location.pathname}${win.location.search}`);
      this.open();
    }
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

  /** The switcher's badges: each project's unread, and the direct conversations'. */
  counts(c: Counts): void {
    const totals = projectTotals(c);
    for (const link of Dom.findAll<HTMLElement>('[data-switch]', this.root)) {
      Dom.setText(Dom.find('[data-slot="unread"]', link), badgeCount(totals.get(link.dataset['switch'] ?? '')?.unread ?? 0));
    }
    Dom.setText(Dom.find('[data-slot="direct"]', this.root), badgeCount(c.unread_direct));
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
