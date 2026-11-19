import { Dom } from '@/core/Dom.ts';
import type { DisposableBag } from '@/core/Disposable.ts';
import { ChatList } from '@/components/ChatList.ts';
import { ConnectionBadge } from '@/components/ConnectionBadge.ts';
import { closeDialog, openDialog } from '@/components/ImageViewer.ts';
import { NavBadges } from '@/components/NavBadges.ts';
import { SideNav } from '@/components/SideNav.ts';
import { Toast, type ToastTone } from '@/components/Toast.ts';
import { Shortcuts } from '@/components/Shortcuts.ts';
import { ApiError } from '@/net/ApiError.ts';
import { LiveFeed } from '@/net/LiveFeed.ts';
import { chatHref } from '@/render/format.ts';
import { CHAT_ROW_ATTRIBUTES, ChatRowRenderer, STREAM_DOT_ATTRIBUTES } from '@/render/renderers/ChatRowRenderer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import type { Services } from '@/App.ts';

/** What every page of the shell has: the toast region, one live feed, and the side list when the page has one. */
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
  const nav = Dom.find('nav.nav', doc);
  if (nav !== null) bag.add(new NavBadges(nav, feed.events, doc).mount());
  let toast: Toast | null = null;
  const region = Dom.find('[data-component="Toast"]', doc);
  if (region !== null) {
    toast = new Toast(region, services.clock, doc);
    bag.add(toast.mount());
  }
  const notify = (text: string, tone: ToastTone = 'info'): void => void toast?.show(text, tone);
  mountSide(doc, services, bag, feed, notify);
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
 * The side list of the project view, on every page that has one: the
 * drawer, the sinks and the switcher (SideNav), its chat rows (ChatList),
 * and the "New channel" dialog of its Chats heading.
 */
function mountSide(doc: Document, services: Services, bag: DisposableBag, feed: LiveFeed, notify: Shell['notify']): void {
  const side = Dom.find('[data-component="SideNav"]', doc);
  if (side === null) return;
  bag.add(new SideNav(side, doc, feed.events, services.clock).mount());
  const list = Dom.find('[data-component="ChatList"]', side);
  if (list !== null) {
    const row = SlotTemplate.find(doc, 'tpl-chat-row', CHAT_ROW_ATTRIBUTES);
    const rows = row === null ? null : new ChatRowRenderer(row, SlotTemplate.find(doc, 'tpl-stream-dot', STREAM_DOT_ATTRIBUTES), () => services.clock.now());
    bag.add(new ChatList(list, rows, feed.events, services.api, services.clock, (t) => notify(t, 'error'), null).mount());
  }
  const dialog = Dom.find<HTMLDialogElement>('[data-component="NewChannel"]', side);
  const button = Dom.find('[data-action="new-channel"]', side);
  if (dialog !== null && button !== null) bindNewChannel(dialog, button, services, bag, (path) => doc.defaultView?.location.assign(path));
}

/** The "New channel" dialog: creates the channel in the side list's project and opens it. */
export function bindNewChannel(dialog: HTMLDialogElement, button: HTMLElement, services: Services, bag: DisposableBag, go: (path: string) => void): void {
  bag.listen(button, 'click', () => openDialog(dialog));
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
