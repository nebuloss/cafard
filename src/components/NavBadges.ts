import { Dom } from '@/core/Dom.ts';
import type { EventBus } from '@/core/EventBus.ts';
import type { FeedEvents } from '@/net/LiveFeed.ts';
import type { Counts } from '@/model/types.ts';
import { Component } from '@/components/Component.ts';

/**
 * Keeps the navigation badges (questions for you on Projects, unread
 * direct messages on Direct, a source down on Admin) and the document
 * title current from the SSE counts event; the server rendered their
 * first values.
 */
export class NavBadges extends Component {
  private readonly baseTitle: string;

  constructor(
    root: HTMLElement,
    private readonly events: EventBus<FeedEvents>,
    private readonly doc: Document,
  ) {
    super(root);
    this.baseTitle = doc.title.replace(/^\(\d+\) /, '');
  }

  protected bind(): void {
    this.bag.add(this.events.on('counts', (c) => this.show(c)));
  }

  show(c: Counts): void {
    const chats = c.open_questions + c.unread_direct;
    for (const [name, n] of [['questions', c.open_questions], ['direct', c.unread_direct]] as const) {
      const badge = Dom.find(`[data-badge="${name}"]`, this.root);
      if (badge !== null) {
        badge.hidden = n === 0;
        Dom.setText(badge, String(n));
      }
    }
    const down = Dom.find('[data-badge="sources-down"]', this.root);
    if (down !== null) down.hidden = c.sources_down === 0;
    this.doc.title = chats > 0 ? `(${chats}) ${this.baseTitle}` : this.baseTitle;
    const nav = this.doc.defaultView?.navigator as (Navigator & { setAppBadge?: (n: number) => Promise<void>; clearAppBadge?: () => Promise<void> }) | undefined;
    if (chats > 0) void nav?.setAppBadge?.(chats).catch(() => {});
    else void nav?.clearAppBadge?.().catch(() => {});
  }
}
