import type { Entry } from '@/model/types.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { eventLink, logTime } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';

export const EVENT_ROW_ATTRIBUTES = ['data-seq', 'data-severity', 'data-kind', 'href'] as const;

/** A line or marker of a bound stream between messages (tpl-event-row). */
export class EventRowRenderer implements EntryRenderer {
  constructor(private readonly template: SlotTemplate) {}

  render(entry: Entry): HTMLElement {
    const t = this.template.clone();
    t.attr({ 'data-seq': entry.seq, 'data-severity': entry.severity, 'data-kind': entry.kind });
    t.text('time', logTime(entry.ts));
    t.slot('time').setAttribute('datetime', entry.ts);
    t.text('stream', entry.stream);
    t.text('text', stripControls(entry.text));
    const href = eventLink(entry.stream, entry.seq);
    if (href !== '') t.slotAttr('open', { href });
    return t.root;
  }
}

/** Escape sequences and control characters out, as view.StripText does. */
export function stripControls(text: string): string {
  return text.replace(/\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[@-_]|[\x00-\x08\x0a-\x1f\x7f-\x9f]/g, '');
}
