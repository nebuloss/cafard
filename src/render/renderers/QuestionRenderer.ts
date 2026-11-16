import type { Entry } from '@/model/types.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { answerText, chatHref, chatLabel, messageTime } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';
import { textSegments } from '@/render/TextSegments.ts';

export const QUESTION_ATTRIBUTES = ['data-seq', 'data-open', 'id', 'href'] as const;
export const CHOICE_ATTRIBUTES = ['data-choice', 'disabled', 'value'] as const;

export interface QuestionOptions {
  readonly now: () => number;
  /** The no-JS form's return pages (home answers in place). */
  readonly back?: string;
  readonly next?: string;
  /** Show the question's chat (lists across chats, tpl-needs-you-question). */
  readonly inList?: boolean;
}

/**
 * Builds a question card from tpl-question: the buttons are the choices of
 * the plain form the server renders, so QuestionCard binds both the same way.
 */
export class QuestionRenderer implements EntryRenderer {
  constructor(
    private readonly template: SlotTemplate,
    private readonly choice: SlotTemplate,
    private readonly options: QuestionOptions,
  ) {}

  render(entry: Entry): HTMLElement {
    const q = entry.question ?? { choices: [], open: true };
    const t = this.template.clone();
    t.attr({ id: `m${entry.seq}`, 'data-seq': entry.seq, 'data-open': q.open });
    t.text('sender', entry.sender?.name ?? '');
    t.time('time', messageTime(entry.ts, this.options.now()), entry.ts);
    t.children('body', ...textSegments(entry.segments, entry.text));
    t.children('choices', ...q.choices.map((c) => this.choice.clone().attr({ 'data-choice': c, value: c, disabled: !q.open }).text('label', c).root));
    t.text('answer', answerText(q.answer));
    const field = (name: string, value: string): void => {
      const input = t.root.querySelector<HTMLInputElement>(`input[data-field="${name}"]`);
      if (input !== null) input.value = value;
    };
    field('chat', entry.chat ?? '');
    field('reply_to', String(entry.seq));
    field('back', this.options.back ?? '');
    field('next', this.options.next ?? '');
    if (this.options.inList === true && entry.chat !== undefined) {
      const chat = t.root.querySelector<HTMLElement>('[data-slot="chat"]');
      if (chat !== null) {
        t.slotAttr('chat', { href: `${chatHref(entry.chat)}#m${entry.seq}` });
        chat.textContent = `in ${chatLabel(entry.chat)}`;
      }
    }
    return t.root;
  }
}
