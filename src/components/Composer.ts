import type { Clock } from '@/core/Clock.ts';
import { Dom } from '@/core/Dom.ts';
import type { UploadQueue } from '@/upload/UploadQueue.ts';
import { jobId } from '@/upload/UploadQueue.ts';
import type { JobData } from '@/upload/UploadJob.ts';
import { Component } from '@/components/Component.ts';
import type { AttachmentTray, PendingLog } from '@/components/AttachmentTray.ts';

/** What a composer's form holds, ready for the queue. */
export interface Draft {
  readonly chat: string;
  readonly text: string;
  readonly replyTo?: number;
  readonly staged?: string;
}

/** Turns a draft and the tray into a job. */
export function jobOf(draft: Draft, tray: AttachmentTray, keepOriginal: boolean, clock: Clock): JobData {
  const { files, logs } = tray.items.value;
  const base: JobData = {
    id: jobId(clock),
    chat: draft.chat,
    text: draft.text,
    files: files.map((f, i) => ({ name: f.name === '' ? `photo-${i + 1}.jpg` : f.name, blob: f })),
    processed: false,
    keepOriginal,
    createdAt: clock.now(),
  };
  return {
    ...base,
    ...(draft.replyTo === undefined ? {} : { replyTo: draft.replyTo }),
    ...(draft.staged === undefined ? {} : { staged: draft.staged }),
    ...(logs.length === 0 ? {} : { logs: logs.map((l) => l.request) }),
  };
}

/** Photos dropped or pasted: the image files of a DataTransfer. */
export function imageFiles(data: DataTransfer | null): File[] {
  if (data === null) return [];
  return Array.from(data.files).filter((f) => f.type.startsWith('image/'));
}

/**
 * The chat composer (PLAN 9.3): text, photos from the picker or the camera,
 * paste and drag and drop, replies, log chips, Ctrl+Enter. Sending goes
 * through the upload queue, so the form never posts while the bundle runs
 * and a message written offline waits there.
 */
export class Composer extends Component<HTMLFormElement> {
  private text!: HTMLTextAreaElement;
  private replyTo: number | undefined;

  constructor(
    root: HTMLFormElement,
    private readonly chat: string,
    private readonly tray: AttachmentTray,
    private readonly queue: UploadQueue,
    private readonly clock: Clock,
    private readonly doc: Document,
    /** Asks for a log slice of a bound stream (the "+" menu). */
    private readonly attach: (stream: string, mode: 'last' | 'from-now') => Promise<PendingLog | null>,
    /** Opens "attach from gallery", when the chat has galleries. */
    private readonly openPicker: (() => void) | null = null,
  ) {
    super(root);
  }

  protected override build(): void {
    this.text = Dom.find<HTMLTextAreaElement>('textarea[name="text"]', this.root) ?? this.doc.createElement('textarea');
  }

  protected bind(): void {
    this.bag.listen(this.root, 'submit', (event) => {
      event.preventDefault();
      void this.send();
    });
    this.bag.listen(this.text, 'keydown', (event) => {
      const e = event as KeyboardEvent;
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        void this.send();
      }
    });
    for (const input of Dom.findAll<HTMLInputElement>('input[type="file"]', this.root)) {
      this.bag.listen(input, 'change', () => {
        this.tray.addFiles(Array.from(input.files ?? []));
        // The files live in the tray now; the form must not post them too.
        input.value = '';
      });
    }
    this.bag.listen(this.root, 'paste', (event) => {
      const files = imageFiles((event as ClipboardEvent).clipboardData);
      if (files.length > 0) {
        event.preventDefault();
        this.tray.addFiles(files);
      }
    });
    this.bag.listen(this.root, 'dragover', (event) => {
      event.preventDefault();
      this.root.dataset['drop'] = '';
    });
    this.bag.listen(this.root, 'dragleave', () => delete this.root.dataset['drop']);
    this.bag.listen(this.root, 'drop', (event) => {
      event.preventDefault();
      delete this.root.dataset['drop'];
      this.tray.addFiles(imageFiles((event as DragEvent).dataTransfer));
    });
    const picker = Dom.find('[data-action="gallery-picker"]', this.root);
    if (picker !== null && this.openPicker !== null) {
      const open = this.openPicker;
      this.bag.listen(picker, 'click', () => open());
    }
    const cancel = Dom.find('[data-action="cancel-reply"]', this.root);
    if (cancel !== null) this.bag.listen(cancel, 'click', () => this.setReply(undefined));
    this.bag.listen(this.root, 'click', (event) => {
      const button = (event.target as Element | null)?.closest<HTMLElement>('[data-action^="attach-"]');
      const stream = button?.dataset['stream'];
      if (button === null || button === undefined || stream === undefined) return;
      button.closest('details')?.removeAttribute('open');
      void this.attach(stream, button.dataset['action'] === 'attach-last' ? 'last' : 'from-now').then((log) => {
        if (log !== null) this.tray.addLog(log);
      });
    });
  }

  /** Replies to seq (undefined cancels), showing the quote above the field. */
  setReply(seq: number | undefined, quote = ''): void {
    this.replyTo = seq;
    const bar = Dom.find('[data-slot="reply"]', this.root);
    Dom.setText(Dom.find('[data-slot="reply-text"]', this.root), quote);
    if (bar !== null) bar.hidden = seq === undefined;
    if (seq !== undefined) this.text.focus();
  }

  /** Writes a gallery ref at the end of the text (the gallery picker). */
  insertRef(ref: string): void {
    const t = this.text.value;
    this.text.value = t === '' || /\s$/.test(t) ? `${t}${ref} ` : `${t} ${ref} `;
    this.text.focus();
  }

  addLog(log: PendingLog): void {
    this.tray.addLog(log);
    this.text.focus();
  }

  focus(): void {
    this.text.focus();
  }

  private keepOriginal(): boolean {
    return Dom.find<HTMLInputElement>('input[data-field="keep-original"]', this.root)?.checked === true;
  }

  async send(): Promise<void> {
    const text = this.text.value;
    if (text.trim() === '' && this.tray.empty) return;
    const job = jobOf({ chat: this.chat, text, ...(this.replyTo === undefined ? {} : { replyTo: this.replyTo }) }, this.tray, this.keepOriginal(), this.clock);
    this.text.value = '';
    this.tray.clear();
    this.setReply(undefined);
    await this.queue.add(job);
  }
}
