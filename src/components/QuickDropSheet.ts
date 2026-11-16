import type { Clock } from '@/core/Clock.ts';
import { Dom } from '@/core/Dom.ts';
import { isString, StorageKey, type KeyValueStore } from '@/core/KeyValueStore.ts';
import type { UploadQueue } from '@/upload/UploadQueue.ts';
import { Component } from '@/components/Component.ts';
import type { AttachmentTray } from '@/components/AttachmentTray.ts';
import { jobOf } from '@/components/Composer.ts';
import { closeDialog, openDialog } from '@/components/ImageViewer.ts';

/** The chat the last quick drop went to (PLAN 9.3). */
export const LAST_DROP = new StorageKey<string>('cafard.lastDropChat', isString);

/**
 * The quick drop form, in the sheet on home and on /drop: a chat picker
 * that starts on the last chat used, the camera, the photo picker, and the
 * files the share target staged. It sends through the upload queue.
 */
export class DropForm extends Component<HTMLFormElement> {
  constructor(
    root: HTMLFormElement,
    private readonly tray: AttachmentTray,
    private readonly queue: UploadQueue,
    private readonly storage: KeyValueStore,
    private readonly clock: Clock,
    private readonly sent: (chat: string) => void,
  ) {
    super(root);
  }

  private get picker(): HTMLSelectElement | null {
    return Dom.find<HTMLSelectElement>('select[name="chat"]', this.root);
  }

  protected bind(): void {
    const last = this.storage.get(LAST_DROP);
    const picker = this.picker;
    if (last !== null && picker !== null && Array.from(picker.options).some((o) => o.value === last)) picker.value = last;
    for (const input of Dom.findAll<HTMLInputElement>('input[type="file"]', this.root)) {
      this.bag.listen(input, 'change', () => {
        this.tray.addFiles(Array.from(input.files ?? []));
        input.value = '';
      });
    }
    this.bag.listen(this.root, 'submit', (event) => {
      event.preventDefault();
      void this.send();
    });
  }

  async send(): Promise<void> {
    const chat = this.picker?.value ?? '';
    const text = Dom.find<HTMLTextAreaElement>('textarea[name="text"]', this.root);
    const staged = Dom.find<HTMLInputElement>('input[name="staged"]', this.root);
    if (chat === '' || ((text?.value ?? '').trim() === '' && this.tray.empty && staged === null)) return;
    const keep = Dom.find<HTMLInputElement>('input[data-field="keep-original"]', this.root)?.checked === true;
    const job = jobOf({ chat, text: text?.value ?? '', ...(staged === null ? {} : { staged: staged.value }) }, this.tray, keep, this.clock);
    this.storage.set(LAST_DROP, chat);
    if (text !== null) text.value = '';
    this.tray.clear();
    // A share is sent once.
    staged?.remove();
    Dom.find('.images.staged', this.root)?.remove();
    await this.queue.add(job);
    this.sent(chat);
  }
}

/** The bottom sheet around the drop form on home. */
export class QuickDropSheet extends Component<HTMLDialogElement> {
  protected bind(): void {
    this.bag.listen(this.root, 'click', (event) => {
      const target = event.target as Element | null;
      if (target === this.root || target?.closest('[data-action="close"]') !== null) closeDialog(this.root);
    });
  }

  open(): void {
    openDialog(this.root);
  }

  close(): void {
    closeDialog(this.root);
  }
}
