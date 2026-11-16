import { Dom } from '@/core/Dom.ts';
import type { EventBus } from '@/core/EventBus.ts';
import type { Answered, Entry } from '@/model/types.ts';
import type { FeedEvents } from '@/net/LiveFeed.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { Component } from '@/components/Component.ts';

/** Whether a new question is for the viewer: by name, or to @humans for a human. */
export function isForMe(e: Entry, viewer: string, human: boolean): boolean {
  if (e.question === undefined || !e.question.open || e.sender?.name === viewer) return false;
  return e.recipient === viewer || (human && e.recipient === '@humans');
}

/**
 * "Needs you" (PLAN 9.6.3): open questions for the viewer, answerable in
 * place, then direct conversations with unread messages. Questions come
 * in from the feed and leave once answered; the section hides when empty.
 */
export class NeedsYou extends Component {
  constructor(
    root: HTMLElement,
    private readonly events: EventBus<FeedEvents>,
    private readonly questions: EntryRenderer | null,
    private readonly viewer: string,
    private readonly human: boolean,
    private readonly muted: (chat: string) => boolean,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.add(this.events.on('entry', (e) => this.add(e)));
    this.bag.add(this.events.on('answered', (a) => this.answered(a)));
    this.update();
  }

  add(e: Entry): void {
    const list = Dom.find('[data-slot="questions"]', this.root);
    if (list === null || this.questions === null || !isForMe(e, this.viewer, this.human)) return;
    if (e.recipient !== this.viewer && e.chat !== undefined && this.muted(e.chat)) return;
    if (Dom.find(`[data-seq="${e.seq}"]`, list) !== null) return;
    const card = this.questions.render(e);
    list.prepend(card);
    this.update();
    card.focus({ preventScroll: true });
  }

  answered(a: Answered): void {
    for (const card of Dom.findAll(`[data-slot="questions"] .question[data-seq="${a.seq}"]`, this.root)) card.remove();
    this.update();
  }

  private update(): void {
    const any = Dom.find('.question, a.chat-row', this.root) !== null;
    this.root.hidden = !any;
  }
}
