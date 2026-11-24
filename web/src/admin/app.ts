import { ApiClient, Refused } from '@shared/api';
import { Component } from '@shared/component';
import { Html, html } from '@shared/html';
import { t } from '@shared/i18n';
import { Icon } from '@shared/icons';
import { Layer } from '@shared/overlay';
import { Toaster } from '@shared/toast';
import { ui } from '@shared/ui';
import { LangMenu, OfflineChip, SourceLink, ThemeSwitch } from '@shared/widgets';
import { LiveStream, Serial } from '@shared/live';
import { UpdateBar } from '@shared/toast';
import { AdminStore } from './store';
import { NoticeCenter, bell } from './notices';
import { MemberPage, ProjectPage } from './pages';
import { canonical, fromHash, navLevel, placeOf, tabLabel, trailOf, type Place } from './route';
import type { View } from './view';
import { AuditView } from './audit';
import { MembersView, OverviewView, ProjectsView, SettingsView, SignInView } from './views';

/**
 * AdminApp runs the admin UI: it signs in, reads the store, and draws the
 * page the URL's path names (route.ts), in a shell with a trail across the
 * top and a rail down the side, as claudication's admin UI does. Dialogs
 * open on its two layers.
 */
export class AdminApp {
  readonly api: ApiClient;
  readonly store: AdminStore;
  /** Dialogs: making a member, a project, a link. */
  readonly layer: Layer;
  /** What opens over a dialog. */
  readonly layer2: Layer;
  /** What the lists remember across pages: the search and the filters. */
  readonly ui = { query: '', kind: 'all', audit: 'all' };
  private shell: Shell | null = null;
  /**
   * What changed, from /admin/api/events: the page reads again, as
   * claudication's admin UI does, instead of every minute.
   */
  readonly live = new LiveStream('/admin/api/events', ['changed'], {
    event: () => void this.reread.run(),
    resync: () => void this.reread.run(),
    updated: () => UpdateBar.show(),
  });
  private readonly reread = new Serial(() => this.liveRefresh());
  private readonly listeners = new Set<() => void>();
  private redrawDue = false;

  constructor(private readonly root: HTMLElement) {
    OfflineChip.bind(this.live);
    document.addEventListener('focusout', () => setTimeout(() => this.drawIfDue(), 0));
    document.addEventListener('scrollend', () => this.drawIfDue(), true);
    document.addEventListener('click', () => setTimeout(() => this.drawIfDue(), 0));
    this.api = new ApiClient('/admin/api', () => this.showSignIn(t('You were signed out. Sign in again.')));
    this.store = new AdminStore(this.api);
    this.layer = new Layer(document.getElementById('overlay') as HTMLElement);
    this.layer2 = new Layer(document.getElementById('overlay2') as HTMLElement);
    window.addEventListener('popstate', () => {
      this.layer2.close();
      this.layer.close();
      void this.refresh();
    });
    // Every link to a page of this UI is followed without a reload; one
    // opened in a new tab, or with a modifier, is the browser's.
    document.addEventListener('click', (e) => {
      const a = (e.target as Element | null)?.closest<HTMLAnchorElement>('a[href^="/"]');
      if (!a || a.target || a.hasAttribute('download') || e.defaultPrevented) return;
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const href = a.getAttribute('href') ?? '';
      if (href.startsWith('/admin/')) return;
      e.preventDefault();
      void this.go(href);
    });
  }

  get place(): Place {
    return placeOf(location.pathname);
  }

  async start(): Promise<void> {
    // Links from before the paths (#members) and paths that name no page
    // land where they meant.
    const target = fromHash(location.hash) ?? canonical(location.pathname);
    if (target !== location.pathname || location.hash) history.replaceState(null, '', target + location.search);
    try {
      if (await this.store.whoAmI()) await this.showShell();
      else this.showSignIn();
    } catch {
      this.root.innerHTML = html`<p class="loading">${t('cafard does not answer. Is it running?')}</p>`.value;
    }
  }

  showSignIn(message?: string): void {
    this.shell = null;
    this.layer2.close();
    this.layer.close();
    const view = new SignInView(this, message);
    this.root.replaceChildren(view.root);
    view.render();
    view.focus();
  }

  async showShell(): Promise<void> {
    this.shell = new Shell(this);
    this.watchNotices();
    this.root.replaceChildren(this.shell.root);
    await this.refresh();
    this.live.start();
  }

  /** Has fn called after each read the stream caused; returns what stops it. */
  onLive(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /**
   * Reads again after a notice. The bell is redrawn at once; the page only
   * when the admin is not in the middle of something: typing in a field,
   * a dialog open, the audit log scrolled down. Then it waits for them to
   * leave the field or close the dialog.
   */
  private async liveRefresh(): Promise<void> {
    if (!this.store.signedIn || !this.shell) return;
    try {
      await this.store.load(this.place.tab);
    } catch {
      return; // the stream will tell again, or the floor read will
    }
    this.redrawBell();
    for (const fn of this.listeners) fn();
    if (this.quiet()) this.shell.render();
    else this.redrawDue = true;
  }

  private quiet(): boolean {
    if (this.layer.isOpen || this.layer2.isOpen) return false;
    const a = document.activeElement;
    if (a instanceof HTMLInputElement || a instanceof HTMLTextAreaElement || a instanceof HTMLSelectElement) return false;
    if (document.querySelector('details[data-menu][open]')) return false;
    return !Array.from(document.querySelectorAll<HTMLElement>('#root .overflow-auto')).some((el) => el.scrollTop > 0);
  }

  /** A redraw that waited for the admin is done when they are done. */
  private drawIfDue(): void {
    if (this.redrawDue && this.shell && this.quiet()) {
      this.redrawDue = false;
      this.shell.render();
    }
  }

  /** Reads the store again and redraws the page. */
  async refresh(): Promise<void> {
    if (!this.store.signedIn || !this.shell) return;
    await this.attempt(() => this.store.load(this.place.tab));
    this.shell.render();
  }

  private noticeTimer: ReturnType<typeof setInterval> | undefined;

  /**
   * Reads the notices again every minute, so the bell counts a link that
   * just expired without the page being touched. The rest of the page waits
   * for the admin to move: redrawing it under them would lose what they type.
   */
  private watchNotices(): void {
    clearInterval(this.noticeTimer);
    this.noticeTimer = setInterval(async () => {
      if (!this.store.signedIn || !this.shell || document.visibilityState !== 'visible') return;
      try {
        await this.store.loadNotices();
      } catch {
        return;
      }
      this.redrawBell();
    }, 60_000);
  }

  /** Draws the bell again, from the store's notices. */
  redrawBell(): void {
    const slot = document.querySelector<HTMLElement>('.bell-slot');
    if (slot) slot.innerHTML = bell(this.store.unread()).value;
  }

  openNotices(): void {
    this.layer.open(new NoticeCenter(this), 'right');
  }

  /** Goes to a page of this UI: a new entry in the history, then the page. */
  async go(path: string): Promise<void> {
    const to = canonical(path);
    if (to !== location.pathname) history.pushState(null, '', to);
    this.layer2.close();
    this.layer.close();
    await this.refresh();
  }

  /**
   * attempt runs an action and shows a refusal as a toast. Every refusal
   * names what to do instead, so its message is the whole of the error
   * handling. done, if given, is shown when it worked.
   */
  async attempt<T>(fn: () => Promise<T>, done?: string): Promise<T | null> {
    try {
      const r = await fn();
      if (done) Toaster.show(done);
      return r;
    } catch (e) {
      if (!(e instanceof Refused && e.status === 401)) Toaster.show(e instanceof Error ? e.message : String(e));
      return null;
    }
  }

  /** attempt, then refresh. */
  async change<T>(fn: () => Promise<T>, done?: string): Promise<T | null> {
    const r = await this.attempt(fn, done);
    await this.refresh();
    return r;
  }

  /** How the trail names an item: a member by its display name. */
  nameOf(kind: 'members' | 'projects', name: string): string {
    if (kind === 'members') {
      const m = this.store.member(name);
      return m?.display_name || name;
    }
    return name;
  }
}

/** A rail entry: a pill, filled when it is the page. */
const RAIL_ITEM =
  'state-layer flex min-h-9 shrink-0 items-center gap-3 rounded-md3-full px-3 font-semibold text-on-surface no-underline md:min-h-10 aria-[current=page]:bg-secondary-container aria-[current=page]:text-on-secondary-container';

/**
 * Shell is the frame: the top bar, the rail of the current level down the
 * side (a row above the page on a phone), the trail of every level above the
 * page, and the page. The rail and the trail are plain links, read off the
 * path, so any of them opens in a new tab.
 */
class Shell extends Component {
  constructor(private readonly app: AdminApp) {
    super('div', 'flex h-full flex-col');
    this.actions = {
      signout: async () => {
        await this.app.attempt(() => this.app.store.signOut());
        this.app.showSignIn(t('You are signed out.'));
      },
      notices: () => this.app.openNotices(),
      lang: (el) => LangMenu.choose(el),
      theme: (el) => ThemeSwitch.choose(el),
    };
  }

  private pageOf(place: Place): View {
    const app = this.app;
    switch (place.tab) {
      case 'members':
        return place.name !== undefined ? new MemberPage(app, place.name) : new MembersView(app);
      case 'projects':
        return place.name !== undefined ? new ProjectPage(app, place.name) : new ProjectsView(app);
      case 'audit':
        return new AuditView(app);
      case 'settings':
        return new SettingsView(app);
      default:
        return new OverviewView(app);
    }
  }

  protected view(): Html {
    const path = canonical(location.pathname);
    const level = navLevel(path);
    const counts: Record<string, number> = { '/members': this.app.store.members.length, '/projects': this.app.store.projects.length };
    const current = (href: string) => Html.raw(href === level.current ? 'aria-current="page"' : '');
    const rail = html`${level.back ? html`<a class="${RAIL_ITEM} mb-1.5 bg-surface-lowest font-medium text-on-surface-variant max-md:mr-1 max-md:mb-0" href="${level.back.href}">${Icon.of('back')}${level.back.label}</a>` : ''}
      ${level.items.map((l) => html`<a class="${RAIL_ITEM}" href="${l.href}" ${current(l.href)}>${l.icon ? Icon.of(l.icon) : ''}<span class="min-w-0 truncate">${l.label}</span>${!level.back && counts[l.href] ? html`<span class="ml-auto text-xs font-bold text-on-surface-variant max-md:ml-1">${counts[l.href]}</span>` : ''}</a>`)}`;
    const trail = trailOf(path, (k, name) => this.app.nameOf(k, name));
    const crumbs = trail.map((c, i) => {
      const last = i === trail.length - 1;
      return html`${i > 0 ? html`<span class="flex opacity-40 [&_.ic]:size-3.5">${Icon.of('chev')}</span>` : ''}<a class="state-layer flex items-center gap-1.5 rounded-md3-full py-1.5 text-[13.5px] font-semibold whitespace-nowrap text-inherit no-underline aria-[current=page]:bg-surface-lowest aria-[current=page]:text-on-surface [&_.ic]:size-[17px] ${i === 0 ? 'px-2' : 'px-3'}" href="${c.href}" ${Html.raw(last ? 'aria-current="page"' : '')} ${i === 0 ? html`aria-label="${c.label}"` : ''}>${i === 0 ? Icon.of('home') : html`${c.icon ? Icon.of(c.icon) : ''}<span>${c.label}</span>`}</a>`;
    });
    // Two rows, as claudication's header: the name and the controls, then
    // the trail across the whole width.
    return html`<header class="shrink-0 px-3 pt-2 pb-3 md:px-4"><div class="flex min-h-10 items-center gap-3"><a class="mr-auto flex shrink-0 items-center gap-2 text-[17px] font-bold text-inherit no-underline [&_svg]:size-[26px] [&_svg]:text-primary" href="/" title="cafard admin">${Icon.logo}<span>cafard</span><small class="text-[13px] font-semibold text-on-surface-variant">admin</small></a>
      <span class="live-slot contents"></span>${SourceLink.view()}${LangMenu.view()}${ThemeSwitch.view()}
      <span class="bell-slot contents">${bell(this.app.store.unread())}</span>
      <button class="${ui.btn('text', 'sm')}" data-act="signout">${t('Sign out')}</button></div>
      <nav class="trail mt-2 flex w-full min-w-0 items-center gap-0.5 overflow-x-auto rounded-md3-full bg-primary-container p-1 text-on-primary-container [scrollbar-width:none]" aria-label="${t('Breadcrumb')}">${crumbs}</nav></header>
      <div class="grid min-h-0 flex-1 grid-rows-[auto_minmax(0,1fr)] gap-3 px-3 md:grid-cols-[13.5rem_minmax(0,1fr)] md:grid-rows-1 md:gap-4 md:px-4"><nav class="rail flex gap-0.5 self-start overflow-auto rounded-md3-full bg-surface-container p-1 [scrollbar-width:none] md:max-h-full md:flex-col md:rounded-md3-xl md:p-2" aria-label="${level.back ? t('Pages') : t('Sections')}">${rail}</nav>
      <main class="min-h-0 min-w-0 overflow-y-auto"><div class="page"></div></main></div>`;
  }

  protected override afterRender(): void {
    const view = this.pageOf(this.app.place);
    this.$('.page').replaceWith(view.root);
    view.render();
    // On a phone the rail is a row and the trail can be wider than the
    // screen: bring the page's own entry, and the trail's end, into view.
    this.find('.rail [aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    const trail = this.find('.trail');
    if (trail) trail.scrollLeft = trail.scrollWidth;
    document.title = `${trailOf(canonical(location.pathname), (k, name) => this.app.nameOf(k, name)).slice(1).map((c) => c.label).reverse().join(' · ') || tabLabel('overview')} · cafard admin`;
  }
}
