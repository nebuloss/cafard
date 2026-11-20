import { Dom } from '@/core/Dom.ts';
import type { DisposableBag } from '@/core/Disposable.ts';
import { ChatList } from '@/components/ChatList.ts';
import { ConnectionBadge } from '@/components/ConnectionBadge.ts';
import { MEMBER_ATTRIBUTES, MembersPanel } from '@/components/MembersPanel.ts';
import { SideNav } from '@/components/SideNav.ts';
import { Toast, type ToastTone } from '@/components/Toast.ts';
import { Shortcuts } from '@/components/Shortcuts.ts';
import { LiveFeed } from '@/net/LiveFeed.ts';
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
 * badges and the sinks (SideNav), and its chat rows (ChatList, and one for
 * a page's own list in main, as /dm has). Its "+" are plain links to the
 * new channel form.
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
