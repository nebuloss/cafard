import { h } from '@/core/Dom.ts';
import type { Entry } from '@/model/types.ts';
import { parseAnsi } from '@/render/AnsiParser.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { chatLabel, logTime, severityLetter, severityName } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';
import { streamTone } from '@/render/StreamTone.ts';

/** The attributes CONTRACTS.md 6.2 lets the client set on tpl-line. */
export const LINE_ATTRIBUTES = ['data-seq', 'data-sev', 'data-tone', 'data-selected', 'data-kind'] as const;

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

/**
 * A message in Live (kinds=message), as a line from tpl-line like
 * pages.MessageLine: the chat in the stream column, "»" for severity, the
 * sender before the plain text. Messages are never parsed for ANSI.
 */
export class MessageLineRenderer implements EntryRenderer {
  constructor(private readonly template: SlotTemplate) {}

  render(entry: Entry): HTMLElement {
    const chat = entry.chat ?? entry.stream;
    const row = this.template.clone();
    row.attr({ 'data-kind': 'message', 'data-seq': entry.seq, 'data-sev': entry.severity, 'data-tone': streamTone(chat) });
    row.text('time', logTime(entry.ts));
    row.slot('time').setAttribute('datetime', entry.ts);
    row.text('stream', chatLabel(chat));
    row.text('sev', '»');
    row.slot('sev').setAttribute('title', 'message');
    const sender = entry.sender?.name;
    row.text('text', sender === undefined || sender === '' ? entry.text : `${sender}: ${entry.text}`);
    return row.root;
  }
}
