import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import { chatLabel } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';
import { ChatList } from '@/components/ChatList.ts';
import { ImageViewer } from '@/components/ImageViewer.ts';
import { NeedsYou } from '@/components/NeedsYou.ts';
import { Overview } from '@/components/Overview.ts';
import { ProjectCards } from '@/components/ProjectCards.ts';
import { QuestionCard } from '@/components/QuestionCard.ts';
import { QuickDropSheet } from '@/components/QuickDropSheet.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell, type Shell } from '@/pages/Shell.ts';
import { chatRenderers, mountDropForm, viewerOf } from '@/pages/chatParts.ts';

/**
 * Home (PLAN 9.6.3, DECISIONS "projects first"): the project cards with
 * "Needs you" on top, the overview on wide screens, and the quick drop
 * sheet behind the FAB (opened by the share target's /?share= too).
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
    const toast = shell.notify;
    const queue = services.uploads;

    const cards = Dom.find('[data-component="ProjectCards"]', doc);
    if (cards !== null) bag.add(new ProjectCards(cards, shell.feed.events, services.clock).mount());
    const mutedChats = new Set((layout?.dataset['muted'] ?? '').split(' ').filter((c) => c !== ''));
    const needs = Dom.find('[data-component="NeedsYou"]', doc);
    if (needs !== null) {
      bag.add(new NeedsYou(needs, shell.feed.events, r.needsYou, viewer.name, viewer.human, (chat) => mutedChats.has(chat)).mount());
      // Its direct conversations' rows keep their badges and previews.
      bag.add(new ChatList(needs, r.rows, shell.feed.events, services.api, services.clock, (t) => toast(t, 'error'), null).mount());
    }
    const main = Dom.find('main', doc);
    if (main !== null) {
      const questions = new QuestionCard(main, queue, services.clock);
      bag.add(questions.mount());
      bag.add(shell.feed.events.on('answered', (a) => questions.answered(a)));
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
    void queue.restore();
    if (win !== null) bag.listen(win, 'online', () => queue.kick());
    bag.add(shell.feed.start());
    return bag;
  }

  private quickDrop(bag: DisposableBag, chip: SlotTemplate | null, upload: SlotTemplate | null, toast: Shell['notify']): void {
    const { doc, services } = this;
    const sheetEl = Dom.find<HTMLDialogElement>('[data-component="QuickDropSheet"]', doc);
    const form = sheetEl === null ? null : Dom.find<HTMLFormElement>('form.drop-form', sheetEl);
    if (sheetEl === null || form === null) return;
    const sheet = new QuickDropSheet(sheetEl);
    bag.add(sheet.mount());
    mountDropForm(form, doc, services, bag, { chip, upload }, () => sheet.close());
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
}
