import type { Clock } from '@/core/Clock.ts';
import type { DisposableBag } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import type { Entry } from '@/model/types.ts';
import { RendererRegistry } from '@/render/RendererRegistry.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { LINE_ATTRIBUTES, LineRenderer } from '@/render/renderers/LineRenderer.ts';
import { MARKER_ATTRIBUTES, MarkerRenderer } from '@/render/renderers/MarkerRenderer.ts';
import { ATTACHMENT_CHIP_ATTRIBUTES, UPLOAD_STATUS_ATTRIBUTES } from '@/render/renderers/AttachmentChipRenderer.ts';
import { CHAT_ROW_ATTRIBUTES, ChatRowRenderer, STREAM_DOT_ATTRIBUTES } from '@/render/renderers/ChatRowRenderer.ts';
import { EVENT_ROW_ATTRIBUTES, EventRowRenderer } from '@/render/renderers/EventRowRenderer.ts';
import { IMAGE_ATTRIBUTES, ImageRenderer } from '@/render/renderers/ImageRenderer.ts';
import { LOG_ATTACHMENT_ATTRIBUTES, LogAttachmentRenderer } from '@/render/renderers/LogAttachmentRenderer.ts';
import { MESSAGE_ATTRIBUTES, MessageRenderer } from '@/render/renderers/MessageRenderer.ts';
import { CHOICE_ATTRIBUTES, QUESTION_ATTRIBUTES, QuestionRenderer } from '@/render/renderers/QuestionRenderer.ts';
import { AttachmentTray } from '@/components/AttachmentTray.ts';
import { DropForm } from '@/components/QuickDropSheet.ts';
import { UploadStatus } from '@/components/UploadStatus.ts';
import type { Services } from '@/App.ts';

/** Who the page is for, from the layout's data attributes. */
export interface Viewer {
  readonly name: string;
  readonly human: boolean;
}

export function viewerOf(el: HTMLElement | null): Viewer {
  return { name: el?.dataset['viewer'] ?? '', human: el?.dataset['viewerKind'] === 'human' };
}

/** The renderers of the chat pages, from the page's twins. */
export interface ChatRenderers {
  /** Messages, questions and event rows: the conversation. */
  readonly timeline: EntryRenderer;
  /** Lines and markers: the logs pane. */
  readonly logs: EntryRenderer | null;
  /** Needs you cards, with their chat. */
  readonly needsYou: EntryRenderer | null;
  readonly rows: ChatRowRenderer | null;
  readonly chip: SlotTemplate | null;
  readonly upload: SlotTemplate | null;
}

/**
 * The timeline renderer: questions by the question card, messages by the
 * message renderer, the rest as event rows; anything a page lacks the
 * template for falls back to a plain event row or line.
 */
export function chatRenderers(doc: Document, viewer: Viewer, clock: Clock, form: { back?: string; next?: string } = {}): ChatRenderers {
  const find = (id: string, attrs: readonly string[]): SlotTemplate | null => SlotTemplate.find(doc, id, attrs);
  const line = find('tpl-line', LINE_ATTRIBUTES);
  const marker = find('tpl-marker', MARKER_ATTRIBUTES);
  const lines = line === null ? null : new RendererRegistry(new LineRenderer(line));
  if (lines !== null && marker !== null) lines.register('marker', new MarkerRenderer(marker));
  const image = find('tpl-image', IMAGE_ATTRIBUTES);
  const log = find('tpl-log-attachment', LOG_ATTACHMENT_ATTRIBUTES);
  const now = (): number => clock.now();
  const eventTpl = find('tpl-event-row', EVENT_ROW_ATTRIBUTES);
  const fallback: EntryRenderer = eventTpl !== null ? new EventRowRenderer(eventTpl) : (lines ?? { render: (e: Entry) => Object.assign(doc.createElement('p'), { textContent: e.text }) });
  const timeline = new RendererRegistry(fallback);
  const choice = find('tpl-choice', CHOICE_ATTRIBUTES);
  const question = find('tpl-question', QUESTION_ATTRIBUTES);
  if (question !== null && choice !== null) timeline.when((e) => e.kind === 'message' && e.question !== undefined, new QuestionRenderer(question, choice, { now, ...form }));
  const message = find('tpl-message', MESSAGE_ATTRIBUTES);
  if (message !== null) {
    timeline.when(
      (e) => e.kind === 'message',
      new MessageRenderer(message, image === null ? null : new ImageRenderer(image), log === null ? null : new LogAttachmentRenderer(log, lines), {
        viewer: viewer.name,
        now,
        quote: (seq) => {
          const parent = Dom.find(`[data-seq="${seq}"]`, doc);
          const sender = parent === null ? '' : (Dom.find('[data-slot="sender"]', parent)?.textContent ?? '');
          const body = parent === null ? '' : (Dom.find('[data-slot="body"]', parent)?.textContent ?? '');
          return sender === '' ? `reply to message ${seq}` : `reply to ${sender}: ${(body.split('\n')[0] ?? '').slice(0, 80)}`;
        },
      }),
    );
  }
  const ny = find('tpl-needs-you-question', QUESTION_ATTRIBUTES);
  const row = find('tpl-chat-row', CHAT_ROW_ATTRIBUTES);
  return {
    timeline,
    logs: lines,
    needsYou: ny === null || choice === null ? null : new QuestionRenderer(ny, choice, { now, back: '/', next: '/', inList: true }),
    rows: row === null ? null : new ChatRowRenderer(row, find('tpl-stream-dot', STREAM_DOT_ATTRIBUTES), now),
    chip: find('tpl-attachment-chip', ATTACHMENT_CHIP_ATTRIBUTES),
    upload: find('tpl-upload-status', UPLOAD_STATUS_ATTRIBUTES),
  };
}

/** Copies text, quietly doing nothing where the clipboard is refused. */
export function copier(win: Window | null): (text: string) => void {
  return (text) => {
    void win?.navigator.clipboard?.writeText(text).catch(() => {});
  };
}

/**
 * A drop form's tray, upload rows and sending, on /drop and in the quick
 * drop sheet. done runs once the form has queued its message.
 */
export function mountDropForm(
  form: HTMLFormElement,
  doc: Document,
  services: Services,
  bag: DisposableBag,
  templates: { chip: SlotTemplate | null; upload: SlotTemplate | null },
  done: () => void,
): AttachmentTray {
  const tray = new AttachmentTray(Dom.find('[data-slot="tray"]', form) ?? doc.createElement('div'), templates.chip, Number(form.dataset['maxFiles'] ?? '10'));
  bag.add(tray.mount());
  const uploads = Dom.find('[data-slot="uploads"]', form);
  if (uploads !== null) bag.add(new UploadStatus(uploads, templates.upload, services.uploads).mount());
  bag.add(new DropForm(form, tray, services.uploads, services.storage, services.clock, done).mount());
  return tray;
}
