import type { Entry, ImageAttachment, LogAttachment } from '@/model/types.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { messageTime, seenText } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';
import { textSegments } from '@/render/TextSegments.ts';
import type { ImageRenderer } from '@/render/renderers/ImageRenderer.ts';
import type { LogAttachmentRenderer } from '@/render/renderers/LogAttachmentRenderer.ts';
import type { GalleryRefRenderer } from '@/render/renderers/GalleryRefRenderer.ts';

export const MESSAGE_ATTRIBUTES = ['data-seq', 'data-mine', 'data-sender-kind', 'id'] as const;

/** Who is looking, and how to name things around a message. */
export interface MessageContext {
  readonly viewer: string;
  readonly now: () => number;
  /** The reply quote of a message answering seq, when it is known. */
  readonly quote?: (seq: number) => string;
}

export function imagesOf(entry: Entry): ImageAttachment[] {
  return (entry.attachments ?? []).filter((a): a is ImageAttachment => a.type === 'image');
}

export function logsOf(entry: Entry): LogAttachment[] {
  return (entry.attachments ?? []).filter((a): a is LogAttachment => a.type === 'log');
}

/** A message from tpl-message, with its attachments. */
export class MessageRenderer implements EntryRenderer {
  constructor(
    private readonly template: SlotTemplate,
    private readonly images: ImageRenderer | null,
    private readonly logs: LogAttachmentRenderer | null,
    private readonly context: MessageContext,
    private readonly refs: GalleryRefRenderer | null = null,
  ) {}

  render(entry: Entry): HTMLElement {
    const t = this.template.clone();
    const sender = entry.sender;
    t.attr({ id: `m${entry.seq}`, 'data-seq': entry.seq, 'data-sender-kind': sender?.kind ?? '', 'data-mine': sender !== undefined && sender.name === this.context.viewer });
    t.text('sender', sender?.name ?? '').text('sender-kind', sender?.kind ?? '');
    t.time('time', messageTime(entry.ts, this.context.now()), entry.ts);
    t.children('body', ...textSegments(entry.segments, entry.text));
    const grid = this.images?.grid(imagesOf(entry)) ?? null;
    const logs = this.logs;
    const refs = this.refs;
    t.children(
      'attachments',
      grid,
      ...(logs === null ? [] : logsOf(entry).map((a) => logs.render(a))),
      ...(refs === null ? [] : (entry.gallery_refs ?? []).map((r) => refs.render(r))),
    );
    t.text('reply-quote', entry.reply_to === undefined ? '' : (this.context.quote?.(entry.reply_to) ?? `reply to message ${entry.reply_to}`));
    t.text('seen-by', seenText(entry.seen_by));
    return t.root;
  }
}
