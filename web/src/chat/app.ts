import { ApiClient, Refused } from '@shared/api';
import { html } from '@shared/html';
import { Layer } from '@shared/overlay';
import { Toaster } from '@shared/toast';
import { ConversationView } from './conversation';
import { ConversationList } from './list';
import { LiveFeed } from './live';
import { LogViewer, ProfileSheet, SignInView } from './sheets';
import { Address, ChatStore } from './store';
import type { Changed, FileInfo } from './types';

/**
 * ChatApp runs the chat app: it signs in, keeps the store current from the
 * event stream, and shows the conversation the URL's hash names
 * (#c/be98/wifi-drops), or a new one (#new). On a phone one pane shows at a
 * time, the list or the conversation; on a desktop both.
 */
export class ChatApp {
  readonly api: ApiClient;
  readonly store: ChatStore;
  readonly layer: Layer;
  readonly layer2: Layer;
  readonly list: ConversationList;
  private view: ConversationView | null = null;
  private log: LogViewer | null = null;
  private readonly live: LiveFeed;
  private frame: HTMLElement | null = null;
  /** Projects whose list is due to be read again, gathered for a moment. */
  private readonly staleProjects = new Set<string>();
  private staleTimer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly root: HTMLElement) {
    this.api = new ApiClient('/api/v1', () => this.showSignIn('You were signed out. Sign in again.'));
    this.store = new ChatStore(this.api);
    this.layer = new Layer(document.getElementById('overlay') as HTMLElement);
    this.layer2 = new Layer(document.getElementById('overlay2') as HTMLElement);
    this.list = new ConversationList(this);
    this.live = new LiveFeed((c) => void this.changed(c));
    window.addEventListener('hashchange', () => void this.route());
  }

  /** The conversation open now, if one is. */
  get currentAddress(): Address | null {
    return this.view?.address ?? null;
  }

  async start(): Promise<void> {
    try {
      if (await this.store.whoAmI()) await this.showApp();
      else this.showSignIn();
    } catch {
      this.root.innerHTML = html`<p class="loading">cafard does not answer. Is it running?</p>`.value;
    }
  }

  showSignIn(message?: string): void {
    this.live.stop();
    this.layer2.close();
    this.layer.close();
    this.view = null;
    this.frame = null;
    const v = new SignInView(this, message);
    this.root.replaceChildren(v.root);
    v.render();
    v.focus();
  }

  async showApp(): Promise<void> {
    await this.attempt(() => this.store.loadAll());
    this.frame = document.createElement('div');
    this.frame.className = 'app';
    this.frame.append(this.list.root);
    this.list.render();
    const shell = document.createElement('div');
    shell.className = 'shell';
    shell.append(this.frame);
    this.root.replaceChildren(shell);
    this.live.start();
    await this.route();
  }

  go(hash: string): void {
    if (location.hash === hash || (hash === '#' && !location.hash)) void this.route();
    else location.hash = hash;
  }

  /** Shows what the hash names. */
  private async route(): Promise<void> {
    if (!this.frame) return;
    this.layer.close();
    const h = decodeURIComponent(location.hash.slice(1));
    let view: ConversationView | null = null;
    if (h.startsWith('c/')) {
      const a = Address.parse(h.slice(2));
      if (a) view = new ConversationView(this, a);
    } else if (h === 'new' || h.startsWith('new/')) {
      view = new ConversationView(this, null, h.slice(4) || undefined);
    }
    this.view = view;
    this.frame.dataset['pane'] = view ? 'chat' : 'list';
    this.frame.querySelector('main')?.remove();
    if (view) {
      this.frame.append(view.root);
      await view.load();
    } else {
      const empty = document.createElement('main');
      empty.className = 'chat';
      empty.innerHTML = html`<div class="empty"><h2>Pick a conversation</h2><p>Or start one with +. Photos, logs and messages go to everyone in its project, agents included.</p></div>`.value;
      this.frame.append(empty);
    }
    this.list.drawRows();
  }

  /** What the event stream said changed: read it again. */
  private async changed(c: Changed): Promise<void> {
    if (c.topic === 'me' || c.topic === 'resync') {
      await this.attempt(() => this.store.loadAll());
      this.list.render();
      if (this.view?.address && !this.store.projects.some((p) => p.name === this.view?.address?.project)) {
        this.go('#');
        return;
      }
      await this.view?.refresh();
      return;
    }
    const project = this.store.projectById(c.project ?? 0);
    if (!project) {
      // A project this page does not know yet: read the list again.
      await this.attempt(() => this.store.loadAll());
      this.list.render();
      return;
    }
    this.markStale(project.name);
    if (c.topic === 'conversation' && c.conversation) {
      const t = this.store.threadById(project.id, c.conversation);
      if (t && this.view?.address?.toString() === t.address.toString()) {
        await this.view.refresh();
        if (this.log) await this.log.update(this.view.fileNow(this.log.fileId));
      } else if (t) {
        // Open before but not on screen: read again when it is next shown.
        this.store.threads.delete(t.address.toString());
      }
    }
  }

  /** Reads a project's conversation list again, once for a burst of notices. */
  private markStale(project: string): void {
    this.staleProjects.add(project);
    clearTimeout(this.staleTimer);
    this.staleTimer = setTimeout(() => {
      const names = [...this.staleProjects];
      this.staleProjects.clear();
      void Promise.all(names.map((n) => this.attempt(() => this.store.loadConversations(n)))).then(() => this.list.drawRows());
    }, 300);
  }

  async refreshProject(project: string): Promise<void> {
    await this.attempt(() => this.store.loadConversations(project));
    this.list.drawRows();
    await this.view?.refresh();
  }

  openLog(f: FileInfo): void {
    this.log = new LogViewer(this, f);
    this.layer.open(this.log, 'full', () => (this.log = null));
  }

  openProfile(): void {
    this.layer.open(new ProfileSheet(this), 'right', undefined, 'sheet');
  }

  /** Runs an action, showing a refusal as a toast; done is shown when it worked. */
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
}
