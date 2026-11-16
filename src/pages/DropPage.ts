import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import { chatHref } from '@/render/format.ts';
import { AttachmentTray } from '@/components/AttachmentTray.ts';
import { DropForm } from '@/components/QuickDropSheet.ts';
import { UploadStatus } from '@/components/UploadStatus.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell } from '@/pages/Shell.ts';
import { chatRenderers, viewerOf } from '@/pages/chatParts.ts';

/** /drop with the bundle: the same form, sent through the upload queue. */
export class DropPage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const { doc, services } = this;
    const win = doc.defaultView;
    const shell = mountShell(doc, services, bag, '/api/events?scope=status');
    const r = chatRenderers(doc, viewerOf(Dom.find('.drop-page', doc)), services.clock);
    const form = Dom.find<HTMLFormElement>('form.drop-form', doc);
    if (form !== null) {
      const tray = new AttachmentTray(Dom.find('[data-slot="tray"]', form) ?? doc.createElement('div'), r.chip, Number(form.dataset['maxFiles'] ?? '10'));
      bag.add(tray.mount());
      const uploads = Dom.find('[data-slot="uploads"]', form);
      if (uploads !== null) bag.add(new UploadStatus(uploads, r.upload, services.uploads).mount());
      bag.add(new DropForm(form, tray, services.uploads, services.storage, services.clock, () => {}).mount());
      // Once sent, the chat shows it.
      bag.add(services.uploads.events.on('sent', ({ entry }) => win?.location.assign(chatHref(entry.chat ?? ''))));
    }
    void services.uploads.restore();
    if (win !== null) bag.listen(win, 'online', () => services.uploads.kick());
    bag.add(shell.feed.start());
    return bag;
  }
}
