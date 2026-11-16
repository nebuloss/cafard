import type { Entry } from '@/model/types.ts';
import { parseAnsi } from '@/render/AnsiParser.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { logTime } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';

export const MARKER_ATTRIBUTES = ['data-seq'] as const;

/** A marker from tpl-marker: a divider row, "stream: text" like pages.Marker. */
export class MarkerRenderer implements EntryRenderer {
  constructor(private readonly template: SlotTemplate) {}

  render(entry: Entry): HTMLElement {
    const row = this.template.clone();
    row.attr({ 'data-seq': entry.seq });
    row.time('time', logTime(entry.ts), entry.ts);
    const plain = parseAnsi(entry.text)
      .map((s) => s.text)
      .join('');
    row.text('text', `${entry.stream}: ${plain}`);
    return row.root;
  }
}
