import type { Clock } from '@/core/Clock.ts';
import { Dom } from '@/core/Dom.ts';
import type { EventBus } from '@/core/EventBus.ts';
import type { Counts, Entry } from '@/model/types.ts';
import type { FeedEvents } from '@/net/LiveFeed.ts';
import { badgeCount, messageTime } from '@/render/format.ts';
import { Component } from '@/components/Component.ts';
import { entryPreview } from '@/components/ChatList.ts';

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

/** A card's preview line: the chat, then the message, like pages.ProjectPreview. */
export function cardPreview(e: Entry): string {
  const chat = e.chat ?? '';
  const name = chat.startsWith('chan:') ? `#${chat.slice(5).split('/')[1] ?? ''}` : `@${chat.slice(3)}`;
  return `${name} ${entryPreview(e)}`;
}

/**
 * Home's cards (DECISIONS "projects first"): each project's badges from
 * counts, and its latest message's time and preview from the feed; the
 * same for the direct conversations' card.
 */
export class ProjectCards extends Component {
  constructor(
    root: HTMLElement,
    private readonly events: EventBus<FeedEvents>,
    private readonly clock: Clock,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.add(this.events.on('counts', (c) => this.counts(c)));
    this.bag.add(this.events.on('entry', (e) => this.entry(e)));
  }

  counts(c: Counts): void {
    const totals = projectTotals(c);
    for (const card of Dom.findAll<HTMLElement>('[data-project]', this.root)) {
      const t = totals.get(card.dataset['project'] ?? '');
      Dom.setText(Dom.find('[data-slot="unread"]', card), badgeCount(t?.unread ?? 0));
      Dom.setText(Dom.find('[data-slot="questions"]', card), badgeCount(t?.questions ?? 0));
    }
    const direct = Dom.find('[data-direct]', this.root);
    if (direct !== null) Dom.setText(Dom.find('[data-slot="unread"]', direct), badgeCount(c.unread_direct));
  }

  entry(e: Entry): void {
    if (e.kind !== 'message' || e.chat === undefined) return;
    const card = e.chat.startsWith('chan:')
      ? Dom.findAll<HTMLElement>('[data-project]', this.root).find((el) => el.dataset['project'] === (e.chat ?? '').slice(5).split('/', 1)[0])
      : (Dom.find<HTMLElement>('[data-direct]', this.root) ?? undefined);
    if (card === undefined) return;
    Dom.setText(Dom.find('[data-slot="preview"]', card), cardPreview(e));
    const time = Dom.find('[data-slot="time"]', card);
    if (time !== null) {
      Dom.setText(time, messageTime(e.ts, this.clock.now()));
      time.setAttribute('datetime', e.ts);
    }
  }
}
