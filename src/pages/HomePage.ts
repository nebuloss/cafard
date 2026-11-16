import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import { ApiError } from '@/net/ApiError.ts';
import { chatHref, chatLabel } from '@/render/format.ts';
import { AttachmentTray } from '@/components/AttachmentTray.ts';
import { ChatList } from '@/components/ChatList.ts';
import { closeDialog, ImageViewer, openDialog } from '@/components/ImageViewer.ts';
import { NeedsYou } from '@/components/NeedsYou.ts';
import { Overview } from '@/components/Overview.ts';
import { QuestionCard } from '@/components/QuestionCard.ts';
import { DropForm, QuickDropSheet } from '@/components/QuickDropSheet.ts';
import { UploadStatus } from '@/components/UploadStatus.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell } from '@/pages/Shell.ts';
import { chatRenderers, viewerOf } from '@/pages/chatParts.ts';

/**
 * Home (PLAN 9.6.3): the chat list with "Needs you" on top, the overview
 * on wide screens, the quick drop sheet behind the FAB (and opened by the
 * share target's /?share=), and a new channel dialog.
 */
export class HomePage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const { doc, services } = this;
    const win = doc.defaultView;
    const shell = mountShell(doc, services, bag, '/api/events?scope=home');
    const layout = Dom.find('.home-layout', doc);
    const viewer = viewerOf(layout);
    const r = chatRenderers(doc, viewer, services.clock, { back: '/', next: '/' });
    const toast = (text: string, tone: 'info' | 'error' = 'info'): void => void shell.toast?.show(text, tone);
    const queue = services.uploads;

    const list = Dom.find('[data-component="ChatList"]', doc);
    if (list !== null) {
      bag.add(new ChatList(list, r.rows, shell.feed.events, services.api, services.clock, (t) => toast(t, 'error'), Dom.find<HTMLInputElement>('[data-slot="search"]', doc)).mount());
    }
    const muted = (chat: string): boolean => Dom.find(`a.chat-row[data-chat="${chat}"]`, doc)?.hasAttribute('data-muted') === true;
    const needs = Dom.find('[data-component="NeedsYou"]', doc);
    if (needs !== null) bag.add(new NeedsYou(needs, shell.feed.events, r.needsYou, viewer.name, viewer.human, muted).mount());
    const main = Dom.find('main', doc);
    if (main !== null) {
      const cards = new QuestionCard(main, queue, services.clock);
      bag.add(cards.mount());
      bag.add(shell.feed.events.on('answered', (a) => cards.answered(a)));
    }
    const overview = Dom.find('[data-component="Overview"]', doc);
    if (overview !== null) bag.add(new Overview(overview, shell.feed.events, r.timeline).mount());
    const viewerEl = Dom.find<HTMLDialogElement>('[data-component="ImageViewer"]', doc);
    if (viewerEl !== null && main !== null) {
      const viewerC = new ImageViewer(viewerEl);
      bag.add(viewerC.mount());
      bag.listen(main, 'click', (event) => {
        const tile = (event.target as Element | null)?.closest<HTMLAnchorElement>('a.image');
        const href = tile?.getAttribute('href') ?? '';
        if (tile === null || tile === undefined || !href.startsWith('/blob/')) return;
        event.preventDefault();
        viewerC.open([{ src: href, alt: 'Photo' }], 0);
      });
    }

    this.quickDrop(bag, r.chip, r.upload, toast);
    this.newChannel(bag);
    void queue.restore();
    if (win !== null) bag.listen(win, 'online', () => queue.kick());
    bag.add(shell.feed.start());
    return bag;
  }

  private quickDrop(bag: DisposableBag, chip: ReturnType<typeof chatRenderers>['chip'], upload: ReturnType<typeof chatRenderers>['upload'], toast: (t: string, tone?: 'info' | 'error') => void): void {
    const { doc, services } = this;
    const sheetEl = Dom.find<HTMLDialogElement>('[data-component="QuickDropSheet"]', doc);
    const form = sheetEl === null ? null : Dom.find<HTMLFormElement>('form.drop-form', sheetEl);
    if (sheetEl === null || form === null) return;
    const sheet = new QuickDropSheet(sheetEl);
    bag.add(sheet.mount());
    const trayEl = Dom.find('[data-slot="tray"]', form);
    const tray = new AttachmentTray(trayEl ?? doc.createElement('div'), chip, Number(form.dataset['maxFiles'] ?? '10'));
    bag.add(tray.mount());
    const uploads = Dom.find('[data-slot="uploads"]', form);
    if (uploads !== null) bag.add(new UploadStatus(uploads, upload, services.uploads).mount());
    bag.add(new DropForm(form, tray, services.uploads, services.storage, services.clock, () => sheet.close()).mount());
    bag.add(services.uploads.events.on('sent', ({ entry }) => toast(`Sent to ${chatLabel(entry.chat ?? '')}.`)));
    const fab = Dom.find('[data-action="quick-drop"]', doc);
    if (fab !== null) {
      bag.listen(fab, 'click', (event) => {
        event.preventDefault();
        sheet.open();
      });
    }
    if (new URLSearchParams(doc.defaultView?.location.search ?? '').has('share')) sheet.open();
  }

  private newChannel(bag: DisposableBag): void {
    const { doc, services } = this;
    const dialog = Dom.find<HTMLDialogElement>('[data-component="NewChannel"]', doc);
    const button = Dom.find('[data-action="new-channel"]', doc);
    if (dialog === null || button === null) return;
    bag.listen(button, 'click', () => openDialog(dialog));
    bag.listen(dialog, 'click', (event) => {
      const action = (event.target as Element | null)?.closest<HTMLElement>('[data-action]')?.dataset['action'];
      if (action === 'close') closeDialog(dialog);
      if (action !== 'create') return;
      const value = (n: string): string => Dom.find<HTMLInputElement>(`input[name="${n}"]`, dialog)?.value.trim() ?? '';
      const patterns = value('patterns').split(',').map((p) => p.trim()).filter((p) => p !== '');
      const error = Dom.find('[data-slot="error"]', dialog);
      services.api
        .createChannel({ name: value('name'), ...(value('title') === '' ? {} : { title: value('title') }), ...(patterns.length === 0 ? {} : { patterns }) })
        .then((chat) => doc.defaultView?.location.assign(chatHref(chat.id)))
        .catch((e: unknown) => {
          if (error !== null) {
            error.hidden = false;
            error.textContent = e instanceof ApiError ? e.message : 'The channel was not created.';
          }
        });
    });
  }
}
