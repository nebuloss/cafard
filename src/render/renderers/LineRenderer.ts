import { h } from '@/core/Dom.ts';
import type { Entry } from '@/model/types.ts';
import { parseAnsi } from '@/render/AnsiParser.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { logTime, severityLetter, severityName } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';
import { streamTone } from '@/render/StreamTone.ts';

/** The attributes CONTRACTS.md 6.2 lets the client set on tpl-line. */
export const LINE_ATTRIBUTES = ['data-seq', 'data-sev', 'data-tone', 'data-selected'] as const;

/** A log line from tpl-line: time, stream tag, severity letter, ANSI text. */
export class LineRenderer implements EntryRenderer {
  constructor(private readonly template: SlotTemplate) {}

  render(entry: Entry): HTMLElement {
    const row = this.template.clone();
    row.attr({ 'data-seq': entry.seq, 'data-sev': entry.severity, 'data-tone': streamTone(entry.stream) });
    row.text('time', logTime(entry.ts));
    row.slot('time').setAttribute('datetime', entry.ts);
    row.text('stream', entry.stream);
    row.text('sev', severityLetter(entry.severity));
    row.slot('sev').setAttribute('title', severityName(entry.severity));
    row.children(
      'text',
      ...parseAnsi(entry.text).map((s) => (s.class === undefined ? s.text : h('span', { class: s.class }, s.text))),
    );
    return row.root;
  }
}
