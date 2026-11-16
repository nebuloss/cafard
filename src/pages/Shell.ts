import { Dom } from '@/core/Dom.ts';
import type { DisposableBag } from '@/core/Disposable.ts';
import { ConnectionBadge } from '@/components/ConnectionBadge.ts';
import { NavBadges } from '@/components/NavBadges.ts';
import { Toast, type ToastTone } from '@/components/Toast.ts';
import { Shortcuts } from '@/components/Shortcuts.ts';
import { LiveFeed } from '@/net/LiveFeed.ts';
import type { Services } from '@/App.ts';

/** What every page of the shell has: the toast region and one live feed. */
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
  bindConfirms(doc, bag);
  // The keys of PLAN 9.6.9, on every page (track D); "?" needs the dialog the chat pages carry.
  const keys = Dom.find('[data-component="ShortcutsDialog"]', doc) ?? doc.body;
  bag.add(new Shortcuts(keys, doc, (path) => doc.defaultView?.location.assign(path), () => Dom.find<HTMLElement>('form.composer textarea, form.drop-form textarea, [data-slot="search"]', doc)).mount());
  // The service worker keeps the shell offline and steers the share target
  // (track D). Browsers only allow it on HTTPS or localhost.
  void doc.defaultView?.navigator.serviceWorker?.register('/sw.js').catch(() => {});
  return { toast, notify: (text, tone = 'info') => void toast?.show(text, tone), feed };
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
