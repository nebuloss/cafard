import { Dom } from '@/core/Dom.ts';
import type { DisposableBag } from '@/core/Disposable.ts';
import { ChatList } from '@/components/ChatList.ts';
import { ConnectionBadge } from '@/components/ConnectionBadge.ts';
import { closeDialog, openDialog } from '@/components/ImageViewer.ts';
import { MEMBER_ATTRIBUTES, MembersPanel } from '@/components/MembersPanel.ts';
import { SideNav } from '@/components/SideNav.ts';
import { Toast, type ToastTone } from '@/components/Toast.ts';
import { Shortcuts } from '@/components/Shortcuts.ts';
import { ApiError } from '@/net/ApiError.ts';
import { LiveFeed } from '@/net/LiveFeed.ts';
import { chatHref } from '@/render/format.ts';
import { CHAT_ROW_ATTRIBUTES, ChatRowRenderer, STREAM_DOT_ATTRIBUTES } from '@/render/renderers/ChatRowRenderer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import type { Services } from '@/App.ts';

/** What every page of the shell has: the toast region, one live feed, the left bar, and a channel's members panel. */
export interface Shell {
  readonly toast: Toast | null;
  /** Shows a toast when the page has the region, else nothing. */
  readonly notify: (text: string, tone?: ToastTone) => void;
  readonly feed: LiveFeed;
}

/**
 * Mounts the shell's components and the page's one SSE connection
 * (feedPath, "/api/events?..."). The caller starts the feed once its own
 * listeners are on.
 */
export function mountShell(doc: Document, services: Services, bag: DisposableBag, feedPath: string): Shell {
  const feed = bag.add(new LiveFeed(services.eventSources, services.clock, services.backoff(), feedPath));
  const badge = Dom.find('[data-component="ConnectionBadge"]', doc);
  if (badge !== null) bag.add(new ConnectionBadge(badge, feed.state).mount());
  let toast: Toast | null = null;
  const region = Dom.find('[data-component="Toast"]', doc);
  if (region !== null) {
    toast = new Toast(region, services.clock, doc);
    bag.add(toast.mount());
  }
  const notify = (text: string, tone: ToastTone = 'info'): void => void toast?.show(text, tone);
  mountSide(doc, services, bag, feed, notify);
  const members = Dom.find('[data-component="MembersPanel"]', doc);
  if (members !== null) {
    const wide = (): boolean => doc.defaultView?.matchMedia?.('(min-width: 840px)').matches ?? true;
    bag.add(new MembersPanel(members, doc, services.api, feed.events, services.clock, services.storage, SlotTemplate.find(doc, 'tpl-member', MEMBER_ATTRIBUTES), wide).mount());
  }
  bindConfirms(doc, bag);
  // The keys of PLAN 9.6.9, on every page (track D); "?" needs the dialog the chat pages carry.
  const keys = Dom.find('[data-component="ShortcutsDialog"]', doc) ?? doc.body;
  bag.add(new Shortcuts(keys, doc, (path) => doc.defaultView?.location.assign(path), () => Dom.find<HTMLElement>('form.composer textarea, form.drop-form textarea, [data-slot="search"]', doc)).mount());
  // The service worker keeps the shell offline and steers the share target
  // (track D). Browsers only allow it on HTTPS or localhost.
  void doc.defaultView?.navigator.serviceWorker?.register('/sw.js').catch(() => {});
  return { toast, notify, feed };
}

/**
 * The left bar, on every signed-in page: the drawer, the trees, the
 * badges and the sinks (SideNav), its chat rows (ChatList, and one for a
 * page's own list in main, as /dm has), and the "New channel" dialog of
 * each project's Chats heading.
 */
function mountSide(doc: Document, services: Services, bag: DisposableBag, feed: LiveFeed, notify: Shell['notify']): void {
  const side = Dom.find('[data-component="SideNav"]', doc);
  if (side === null) return;
  bag.add(new SideNav(side, doc, feed.events, services.clock, services.storage).mount());
  const row = SlotTemplate.find(doc, 'tpl-chat-row', CHAT_ROW_ATTRIBUTES);
  const rows = row === null ? null : new ChatRowRenderer(row, SlotTemplate.find(doc, 'tpl-stream-dot', STREAM_DOT_ATTRIBUTES), () => services.clock.now());
  // The bar's rows, and a page's own list of them (/dm).
  for (const list of [Dom.find('[data-component="ChatList"]', side), ...Dom.findAll('main [data-component="ChatList"]', doc)]) {
    if (list !== null) bag.add(new ChatList(list, rows, feed.events, services.api, services.clock, (t) => notify(t, 'error'), null).mount());
  }
  const dialog = Dom.find<HTMLDialogElement>('[data-component="NewChannel"]', side);
  const buttons = Dom.findAll('[data-action="new-channel"]', side);
  if (dialog !== null && buttons.length > 0) bindNewChannel(dialog, buttons, services, bag, (path) => doc.defaultView?.location.assign(path));
}

/** The "New channel" dialog: each project's button opens it on that project; it creates the channel and opens it. */
export function bindNewChannel(dialog: HTMLDialogElement, buttons: readonly HTMLElement[], services: Services, bag: DisposableBag, go: (path: string) => void): void {
  for (const button of buttons) {
    bag.listen(button, 'click', () => {
      const project = Dom.find<HTMLInputElement>('input[name="project"]', dialog);
      if (project !== null) project.value = button.dataset['project'] ?? '';
      Dom.setText(Dom.find('[data-slot="project-label"]', dialog), button.dataset['label'] ?? '');
      openDialog(dialog);
    });
  }
  bag.listen(dialog, 'click', (event) => {
    const action = (event.target as Element | null)?.closest<HTMLElement>('[data-action]')?.dataset['action'];
    if (action === 'close') closeDialog(dialog);
    if (action !== 'create') return;
    const value = (n: string): string => Dom.find<HTMLInputElement>(`input[name="${n}"]`, dialog)?.value.trim() ?? '';
    const patterns = value('patterns').split(',').map((p) => p.trim()).filter((p) => p !== '');
    const error = Dom.find('[data-slot="error"]', dialog);
    services.api
      .createChannel({
        name: value('name'),
        ...(value('project') === '' ? {} : { project: value('project') }),
        ...(value('title') === '' ? {} : { title: value('title') }),
        ...(patterns.length === 0 ? {} : { patterns }),
      })
      .then((chat) => go(chatHref(chat.id)))
      .catch((e: unknown) => {
        if (error !== null) {
          error.hidden = false;
          error.textContent = e instanceof ApiError ? e.message : 'The channel was not created.';
        }
      });
  });
}

/** Forms with data-confirm ask before they post. */
export function bindConfirms(doc: Document, bag: DisposableBag): void {
  bag.listen(doc, 'submit', (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    const question = form.dataset['confirm'];
    if (question === undefined) return;
    if (doc.defaultView?.confirm(question) !== true) event.preventDefault();
  });
}
