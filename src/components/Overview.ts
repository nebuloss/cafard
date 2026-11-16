import { Dom } from '@/core/Dom.ts';
import type { EventBus } from '@/core/EventBus.ts';
import type { Answered, Entry } from '@/model/types.ts';
import type { FeedEvents } from '@/net/LiveFeed.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { answerText } from '@/render/format.ts';
import { Component } from '@/components/Component.ts';
import { closeCard } from '@/components/QuestionCard.ts';

/**
 * The overview pane of home on wide screens: recent activity across the
 * followed chats grows from the feed (at most 30 rows), and its question
 * cards close when answered.
 */
export class Overview extends Component {
  static readonly max = 30;

  constructor(
    root: HTMLElement,
    private readonly events: EventBus<FeedEvents>,
    private readonly renderer: EntryRenderer,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.add(this.events.on('entry', (e) => this.add(e)));
    this.bag.add(this.events.on('answered', (a) => this.answered(a)));
  }

  add(e: Entry): void {
    if (e.kind !== 'message') return;
    const recent = Dom.find('[data-slot="recent"]', this.root);
    if (recent === null || Dom.find(`[data-seq="${e.seq}"]`, recent) !== null) return;
    recent.append(this.renderer.render(e));
    while (recent.children.length > Overview.max) recent.firstElementChild?.remove();
    Dom.find('.empty', this.root)?.remove();
  }

  answered(a: Answered): void {
    for (const card of Dom.findAll(`.question[data-seq="${a.seq}"]`, this.root)) closeCard(card, answerText(a.answer));
    for (const card of Dom.findAll(`[data-slot="questions"] .question[data-seq="${a.seq}"]`, this.root)) card.remove();
  }
}
