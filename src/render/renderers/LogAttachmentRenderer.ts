import type { LogAttachment } from '@/model/types.ts';
import { linesText, logLink, logTime } from '@/render/format.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';

export const LOG_ATTACHMENT_ATTRIBUTES = ['href', 'data-snapshot', 'data-expired', 'data-open-ended'] as const;

/** A log attachment card from tpl-log-attachment, its preview lines from tpl-line. */
export class LogAttachmentRenderer {
  constructor(
    private readonly template: SlotTemplate,
    private readonly lines: EntryRenderer | null,
  ) {}

  render(a: LogAttachment): HTMLElement {
    const t = this.template.clone();
    const openEnded = a.to_seq === null;
    t.attr({ 'data-snapshot': a.snapshot, 'data-expired': a.expired, 'data-open-ended': openEnded });
    t.text('streams', a.streams.length > 0 ? a.streams.join(', ') : (a.stream ?? a.channel ?? ''));
    t.text('title', a.title ?? '');
    t.text('lines', linesText(a.lines, openEnded));
    t.text('span', a.first_ts !== undefined && a.last_ts !== undefined ? `${logTime(a.first_ts)} to ${logTime(a.last_ts)}` : '');
    t.text('state', a.expired ? 'lines expired' : openEnded ? 'open-ended' : a.snapshot ? 'snapshot' : '');
    const lines = this.lines;
    t.children('preview', ...(lines === null ? [] : a.preview.map((e) => lines.render(e))));
    t.slotAttr('open', { href: logLink(a) });
    return t.root;
  }
}
