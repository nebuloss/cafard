import { Html, escape } from '@shared/html';

/**
 * MessageText turns a message's text into markup: `code` in backticks,
 * @name of a member, and links. Everything else is escaped, and the order
 * matters: the text is escaped first, so nothing a member wrote can become
 * markup, and the patterns are applied to the escaped text.
 */
export class MessageText {
  constructor(private readonly members: ReadonlySet<string>) {}

  render(text: string): Html {
    const parts = escape(text).split(/(`[^`\n]+`)/g);
    return Html.raw(parts.map((p) => (p.startsWith('`') && p.endsWith('`') && p.length > 2 ? `<code>${p.slice(1, -1)}</code>` : this.inline(p))).join(''));
  }

  private inline(s: string): string {
    return s
      .replace(/(^|[\s(])(https?:\/\/[^\s<>"']+[^\s<>"'.,;:!?)])/g, (_m, pre: string, url: string) =>
        `${pre}<a href="${url}" target="_blank" rel="noopener noreferrer">${url}</a>`)
      .replace(/(^|[\s(])@([a-z0-9][a-z0-9._-]*)/g, (m, pre: string, name: string) =>
        this.members.has(name) ? `${pre}<span class="mention">@${name}</span>` : m);
  }

  /** The @name being typed at the end of before, or null. */
  static mentionAt(before: string): string | null {
    const m = /(?:^|\s)@([a-z0-9._-]*)$/i.exec(before);
    return m ? (m[1] ?? '').toLowerCase() : null;
  }
}
