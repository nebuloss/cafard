import type { Chat } from '@/model/types.ts';
import { chatHref, messageTime, previewText } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';

export const CHAT_ROW_ATTRIBUTES = ['href', 'data-chat', 'data-pinned', 'data-muted', 'data-archived'] as const;
export const STREAM_DOT_ATTRIBUTES = ['data-status', 'title'] as const;

function count(n: number): string {
  if (n <= 0) return '';
  return n > 99 ? '99+' : String(n);
}

/** A row of the chat list from tpl-chat-row, like pages.ChatRow. */
export class ChatRowRenderer {
  constructor(
    private readonly template: SlotTemplate,
    private readonly dot: SlotTemplate | null,
    private readonly now: () => number,
  ) {}

  render(chat: Chat): HTMLElement {
    const t = this.template.clone();
    t.attr({ href: chatHref(chat.id), 'data-chat': chat.id, 'data-pinned': chat.pinned, 'data-muted': chat.muted, 'data-archived': chat.archived });
    this.fill(t.root, chat);
    return t.root;
  }

  /** Refreshes a row in place (a chat event). */
  fill(row: HTMLElement, chat: Chat): void {
    const set = (slot: string, text: string): void => {
      const el = row.querySelector(`[data-slot="${slot}"]`);
      if (el !== null && el.textContent !== text) el.textContent = text;
    };
    set('name', `${chat.kind === 'direct' ? '@' : '#'}${chat.name}`);
    set('preview', chat.preview === undefined ? '' : previewText(chat.preview));
    set('time', chat.last_activity === undefined ? '' : messageTime(chat.last_activity, this.now()));
    row.querySelector('[data-slot="time"]')?.setAttribute('datetime', chat.last_activity ?? '');
    set('unread', chat.muted ? '' : count(chat.unread));
    set('questions', count(chat.open_questions));
    for (const [name, on] of [['data-pinned', chat.pinned], ['data-muted', chat.muted], ['data-archived', chat.archived]] as const) {
      if (on) row.setAttribute(name, '');
      else row.removeAttribute(name);
    }
    const dots = row.querySelector('[data-slot="dots"]');
    const dot = this.dot;
    if (dots !== null && dot !== null) {
      dots.replaceChildren(...chat.streams.map((s) => dot.clone().attr({ 'data-status': s.status, title: `${s.id}: ${s.status}` }).root));
    }
  }

  /** Updates the badges of a row from the counts event. */
  static badges(row: HTMLElement, unread: number, questions: number): void {
    const muted = row.hasAttribute('data-muted');
    const u = row.querySelector('[data-slot="unread"]');
    const q = row.querySelector('[data-slot="questions"]');
    if (u !== null) u.textContent = muted ? '' : count(unread);
    if (q !== null) q.textContent = count(questions);
  }
}
