import { ApiClient, Refused } from '@shared/api';
import { Component } from '@shared/component';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { Layer } from '@shared/overlay';
import { Toaster } from '@shared/toast';
import { ui } from '@shared/ui';
import { AdminStore } from './store';
import { MemberPage, ProjectPage } from './pages';
import { canonical, fromHash, navLevel, placeOf, trailOf, type Place } from './route';
import type { View } from './view';
import { AuditView, MembersView, OverviewView, ProjectsView, SettingsView, SignInView } from './views';

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

  constructor(private readonly root: HTMLElement) {
    this.api = new ApiClient('/admin/api', () => this.showSignIn('You were signed out. Sign in again.'));
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
      this.root.innerHTML = html`<p class="loading">cafard does not answer. Is it running?</p>`.value;
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
    this.root.replaceChildren(this.shell.root);
    await this.refresh();
  }

  /** Reads the store again and redraws the page. */
  async refresh(): Promise<void> {
    if (!this.store.signedIn || !this.shell) return;
    await this.attempt(() => this.store.load(this.place.tab));
    this.shell.render();
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
        this.app.showSignIn('You are signed out.');
      },
    };
  }

  private pageOf(place: Place): View {
    const app = this.app;
    switch (place.tab) {
      case 'members':
        return place.name !== undefined ? new MemberPage(app, place.name, place.page) : new MembersView(app);
      case 'projects':
        return place.name !== undefined ? new ProjectPage(app, place.name, place.page) : new ProjectsView(app);
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
    const trail = trailOf(path, (k, n) => this.app.nameOf(k, n));
    const crumbs = trail.map((c, i) => {
      const last = i === trail.length - 1;
      return html`${i > 0 ? html`<span class="flex opacity-40 [&_.ic]:size-3.5">${Icon.of('chev')}</span>` : ''}<a class="state-layer flex items-center gap-1.5 rounded-md3-full py-1.5 text-[13.5px] font-semibold whitespace-nowrap text-inherit no-underline aria-[current=page]:bg-surface-lowest aria-[current=page]:text-on-surface [&_.ic]:size-[17px] ${i === 0 ? 'px-2' : 'px-3'}" href="${c.href}" ${Html.raw(last ? 'aria-current="page"' : '')} ${Html.raw(i === 0 ? 'aria-label="Overview"' : '')}>${i === 0 ? Icon.of('home') : html`${c.icon ? Icon.of(c.icon) : ''}<span>${c.label}</span>`}</a>`;
    });
    // Two rows, as claudication's header: the name and the controls, then
    // the trail across the whole width.
    return html`<header class="shrink-0 px-3 pt-2 pb-3 md:px-4"><div class="flex min-h-10 items-center gap-3"><a class="mr-auto flex shrink-0 items-center gap-2 text-[17px] font-bold text-inherit no-underline [&_svg]:size-[26px] [&_svg]:text-primary" href="/" title="cafard admin">${Icon.logo}<span>cafard</span><small class="text-[13px] font-semibold text-on-surface-variant">admin</small></a>
      <button class="${ui.btn('text', 'sm')}" data-act="signout">Sign out</button></div>
      <nav class="trail mt-2 flex w-full min-w-0 items-center gap-0.5 overflow-x-auto rounded-md3-full bg-primary-container p-1 text-on-primary-container [scrollbar-width:none]" aria-label="Breadcrumb">${crumbs}</nav></header>
      <div class="grid min-h-0 flex-1 grid-rows-[auto_minmax(0,1fr)] gap-3 px-3 md:grid-cols-[13.5rem_minmax(0,1fr)] md:grid-rows-1 md:gap-4 md:px-4"><nav class="rail flex gap-0.5 self-start overflow-auto rounded-md3-full bg-surface-container p-1 [scrollbar-width:none] md:max-h-full md:flex-col md:rounded-md3-xl md:p-2" aria-label="${level.back ? 'Pages' : 'Sections'}">${rail}</nav>
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
    document.title = `${trailOf(canonical(location.pathname), (k, n) => this.app.nameOf(k, n)).slice(1).map((c) => c.label).reverse().join(' · ') || 'Overview'} · cafard admin`;
  }
}
