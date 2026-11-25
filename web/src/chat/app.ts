import { ApiClient, Refused } from '@shared/api';
import { html } from '@shared/html';
import { t } from '@shared/i18n';
import { Layer } from '@shared/overlay';
import { Toaster } from '@shared/toast';
import { ConversationView } from './conversation';
import { ConversationList } from './list';
import { look } from './look';
import { LiveStream, Serial } from '@shared/live';
import { UpdateBar } from '@shared/toast';
import { OfflineChip } from '@shared/widgets';
import { LogViewer, ProfileSheet, SignInView } from './sheets';
import { fromHash, pathOf, placeOf } from './route';
import { Address, ChatStore } from './store';
import type { Changed, FileInfo } from './types';

/**
 * ChatApp runs the chat app: it signs in, keeps the store current from the
 * event stream, and shows the conversation the URL's path names
 * (/be98/wifi-drops), or a new one (/new; route.ts). On a phone one pane
 * shows at a time, the list or the conversation; on a desktop both.
 */
export class ChatApp {
  readonly api: ApiClient;
  readonly store: ChatStore;
  readonly layer: Layer;
  readonly layer2: Layer;
  readonly list: ConversationList;
  private view: ConversationView | null = null;
  private log: LogViewer | null = null;
  private readonly live: LiveStream;
  /** Notices waiting for the read before them to end. */
  private heard: Changed[] = [];
  private readonly drain = new Serial(() => this.drainHeard());
  private frame: HTMLElement | null = null;
  /** Projects whose list is due to be read again, gathered for a moment. */
  private readonly staleProjects = new Set<string>();
  private staleTimer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly root: HTMLElement) {
    this.api = new ApiClient('/api/v1', () => this.showSignIn(t('This sign-in has ended. Sign in again.')));
    this.store = new ChatStore(this.api);
    this.layer = new Layer(document.getElementById('overlay') as HTMLElement);
    this.layer2 = new Layer(document.getElementById('overlay2') as HTMLElement);
    this.list = new ConversationList(this);
    this.live = new LiveStream('/api/v1/events', ['changed'], {
      event: (_name, data) => this.hear(data as Changed),
      resync: () => this.hear({ topic: 'resync' }),
      updated: () => UpdateBar.show(),
    });
    OfflineChip.bind(this.live);
    window.addEventListener('popstate', () => void this.route());
  }

  /** The conversation open now, if one is. */
  get currentAddress(): Address | null {
    return this.view?.address ?? null;
  }

  async start(): Promise<void> {
    // A link from before paths (#c/be98/x) lands where it meant.
    const old = fromHash(location.hash);
    if (old) history.replaceState(null, '', old);
    try {
      if (await this.store.whoAmI()) await this.showApp();
      else this.showSignIn();
    } catch {
      this.root.innerHTML = html`<p class="${look.loading}">${t('cafard does not answer. Is it running?')}</p>`.value;
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
    this.frame.className = 'group/app grid min-h-0 flex-1 grid-cols-1 min-[840px]:grid-cols-[340px_1fr]';
    this.frame.append(this.list.root);
    this.list.render();
    const shell = document.createElement('div');
    shell.className = 'flex h-full flex-col';
    shell.append(this.frame);
    this.root.replaceChildren(shell);
    this.live.start();
    await this.route();
  }

  /**
   * Goes to a path of the app: a new entry in the history, marked as this
   * app's, then what it shows.
   */
  go(path: string): void {
    if (location.pathname !== path) history.pushState({ inApp: true }, '', path);
    void this.route();
  }

  /** Goes to path in place of where the page is, as for a conversation that is gone. */
  replace(path: string): void {
    history.replaceState(null, '', path);
    void this.route();
  }

  /**
   * Back to the conversations, as a phone's own back does: one step back when
   * the conversation was opened from the list, so going back again does not
   * open it once more; the list in its place when it was opened from a link.
   */
  back(): void {
    if ((history.state as { inApp?: boolean } | null)?.inApp) history.back();
    else this.replace(pathOf({ kind: 'list' }));
  }

  /** Shows what the path names. */
  private async route(): Promise<void> {
    if (!this.frame) return;
    this.layer.close();
    const place = placeOf(location.pathname);
    let view: ConversationView | null = null;
    if (place.kind === 'conversation') view = new ConversationView(this, new Address(place.project, place.name));
    else if (place.kind === 'new') view = new ConversationView(this, null, place.project);
    this.view = view;
    this.frame.dataset['pane'] = view ? 'chat' : 'list';
    this.frame.querySelector('main')?.remove();
    if (view) {
      this.frame.append(view.root);
      await view.load();
    } else {
      const empty = document.createElement('main');
      empty.className = look.pane;
      empty.innerHTML = html`<div class="${look.empty}"><h2>${t('Pick a conversation')}</h2></div>`.value;
      this.frame.append(empty);
    }
    this.list.drawRows();
  }

  /**
   * Takes a notice, read in turn: never two reads at once, and a resync
   * among those waiting stands for all of them.
   */
  private hear(c: Changed): void {
    this.heard.push(c);
    void this.drain.run();
  }

  private async drainHeard(): Promise<void> {
    while (this.heard.length) {
      const batch: Changed[] = this.heard.some((c) => c.topic === 'resync') ? [{ topic: 'resync' }] : this.heard;
      this.heard = [];
      for (const c of batch) await this.changed(c);
    }
  }

  /** What the event stream said changed: read it again. */
  private async changed(c: Changed): Promise<void> {
    if (c.topic === 'me' || c.topic === 'resync') {
      await this.attempt(() => this.store.loadAll());
      this.list.render();
      if (this.view?.address && !this.store.projects.some((p) => p.name === this.view?.address?.project)) {
        this.go(pathOf({ kind: 'list' }));
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
      const th = this.store.threadById(project.id, c.conversation);
      if (th && this.view?.address?.toString() === th.address.toString()) {
        await this.view.refresh();
        if (this.log) await this.log.update(this.view.fileNow(this.log.fileId));
      } else if (th) {
        // Open before but not on screen: read again when it is next shown.
        this.store.threads.delete(th.address.toString());
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
      void Promise.all(names.map((name) => this.attempt(() => this.store.loadConversations(name)))).then(() => this.list.drawRows());
    }, 300);
  }

  async refreshProject(project: string): Promise<void> {
    await this.attempt(() => this.store.loadConversations(project));
    this.list.drawRows();
    await this.view?.refresh();
  }

  openLog(f: FileInfo): void {
    this.log = new LogViewer(this, f);
    this.layer.open(this.log, 'full', () => (this.log = null), look.fullSheet);
  }

  openProfile(): void {
    this.layer.open(new ProfileSheet(this), 'right', undefined, look.sideSheet);
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
