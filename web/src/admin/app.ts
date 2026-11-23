import { ApiClient, Refused } from '@shared/api';
import { Component } from '@shared/component';
import { Html, html } from '@shared/html';
import { Icon, type IconName } from '@shared/icons';
import { Layer } from '@shared/overlay';
import { Toaster } from '@shared/toast';
import { AdminStore } from './store';
import { AuditView, ConfigView, MembersView, ProjectsView, SignInView, StorageView, type View } from './views';
import { MemberDrawer, ProjectDrawer } from './drawers';

export type ViewName = 'members' | 'projects' | 'audit' | 'storage' | 'config';

const VIEWS: Record<ViewName, { label: string; icon: IconName; make: (app: AdminApp) => View }> = {
  members: { label: 'Members', icon: 'people', make: (a) => new MembersView(a) },
  projects: { label: 'Projects', icon: 'folder', make: (a) => new ProjectsView(a) },
  audit: { label: 'Audit log', icon: 'list', make: (a) => new AuditView(a) },
  storage: { label: 'Storage', icon: 'disk', make: (a) => new StorageView(a) },
  config: { label: 'Configuration', icon: 'gear', make: (a) => new ConfigView(a) },
};

/**
 * AdminApp runs the admin UI: it signs in, reads the store, draws the view
 * the URL's hash names, and opens drawers and dialogs on its two layers.
 */
export class AdminApp {
  readonly api: ApiClient;
  readonly store: AdminStore;
  /** Drawers and dialogs. */
  readonly layer: Layer;
  /** What opens over a drawer: the member picker. */
  readonly layer2: Layer;
  /** What the views remember across redraws: the search and the filters. */
  readonly ui = { query: '', kind: 'all', audit: 'all' };
  private shell: Shell | null = null;
  private drawer: MemberDrawer | ProjectDrawer | null = null;

  constructor(private readonly root: HTMLElement) {
    this.api = new ApiClient('/admin/api', () => this.showSignIn('You were signed out. Sign in again.'));
    this.store = new AdminStore(this.api);
    this.layer = new Layer(document.getElementById('overlay') as HTMLElement);
    this.layer2 = new Layer(document.getElementById('overlay2') as HTMLElement);
    window.addEventListener('hashchange', () => {
      this.layer.close();
      void this.refresh();
    });
  }

  get view(): ViewName {
    const h = location.hash.slice(1);
    return h in VIEWS ? (h as ViewName) : 'members';
  }

  async start(): Promise<void> {
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

  /** Reads the store again and redraws the page and any open drawer. */
  async refresh(): Promise<void> {
    if (!this.store.signedIn || !this.shell) return;
    await this.attempt(() => this.store.load(this.view));
    this.shell.render();
    if (this.drawer && this.layer.isOpen) this.drawer.render();
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

  openMember(name: string): void {
    this.drawer = new MemberDrawer(this, name);
    this.layer.open(this.drawer, 'right', () => (this.drawer = null));
  }

  openProject(name: string): void {
    this.drawer = new ProjectDrawer(this, name);
    this.layer.open(this.drawer, 'right', () => (this.drawer = null));
  }

  closeDrawer(): void {
    this.layer.close();
  }

  go(view: ViewName): void {
    location.hash = view;
  }
}

/** Shell is the frame: the top bar, the navigation, and the current view. */
class Shell extends Component {
  constructor(private readonly app: AdminApp) {
    super('div', 'shell');
    this.actions = {
      signout: async () => {
        await this.app.attempt(() => this.app.store.signOut());
        this.app.showSignIn('You are signed out.');
      },
    };
  }

  protected view(): Html {
    const current = this.app.view;
    const counts: Partial<Record<ViewName, number>> = { members: this.app.store.members.length, projects: this.app.store.projects.length };
    const nav = (Object.keys(VIEWS) as ViewName[]).map((k) => {
      const v = VIEWS[k];
      const n = counts[k];
      return html`<a href="#${k}" ${Html.raw(k === current ? 'aria-current="page"' : '')}>${Icon.of(v.icon)}${v.label}${n ? html`<span class="n">${n}</span>` : ''}</a>`;
    });
    return html`<header class="top"><div class="brand">${Icon.logo} cafard <small>admin</small></div>
      <span class="where" title="This UI is served only on admin-listen">${Icon.of('lock')}Internal network only</span>
      <span class="grow"></span>
      <span class="me"><span>admin</span><button data-act="signout">Sign out</button></span></header>
      <div class="body"><nav class="nav" aria-label="Admin sections">${nav}</nav><main class="main"><div class="page"></div></main></div>`;
  }

  protected override afterRender(): void {
    const view = VIEWS[this.app.view].make(this.app);
    this.$('.page').replaceWith(view.root);
    view.render();
  }
}
