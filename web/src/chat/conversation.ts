import { Component } from '@shared/component';
import { Bytes } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { Toaster } from '@shared/toast';
import { Avatar } from '@shared/widgets';
import type { ChatApp } from './app';
import { Composer } from './composer';
import { DetailsSheet, ImageViewer } from './sheets';
import { Address, type Thread } from './store';
import { MessageText } from './text';
import type { FileInfo, Member, Message, Project } from './types';

const SAFE_IMAGES = new Set(['image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif']);
const hhmm = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });

function dayLabel(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return 'Today';
  const y = new Date(now);
  y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return 'Yesterday';
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' });
}

/** A text file the timeline shows as a card with its last lines. */
export function isLog(f: FileInfo): boolean {
  return !!f.lines_url;
}

/**
 * ConversationView is the right pane: one conversation's header, its
 * timeline, and the composer. With no address it is a new conversation,
 * started by sending to it in a project chosen above the composer.
 */
export class ConversationView extends Component {
  private thread: Thread | null = null;
  private members: Member[] = [];
  private readonly composer: Composer;
  private renaming = false;
  private project: string;
  /** The last three lines of each log card, by file id, with the size they were read at. */
  private readonly tails = new Map<number, { size: number; lines: string[] }>();
  private dragDepth = 0;

  constructor(
    private readonly app: ChatApp,
    readonly address: Address | null,
    project?: string,
  ) {
    super('main', 'chat');
    this.project = address?.project ?? project ?? this.openProjects()[0]?.name ?? '';
    this.composer = new Composer({
      send: (text, files) => this.send(text, files),
      members: () => this.members.filter((m) => m.name !== this.app.store.me?.name),
      placeholder: address ? 'Message, or drop a file' : 'Write the first message, or drop a file',
    });
    this.actions = {
      back: () => this.app.go('#'),
      rename: () => {
        this.renaming = true;
        this.drawHead();
      },
      details: () => {
        if (this.address) this.app.layer.open(new DetailsSheet(this.app, this.address), 'right', undefined, 'sheet');
      },
      earlier: async () => {
        await this.app.attempt(() => this.thread?.earlier() ?? Promise.resolve());
        this.drawTimeline(false);
      },
      img: (t) => this.openImage(Number(t.dataset['v']), Number(t.dataset['m'])),
      log: (t) => this.openLog(Number(t.dataset['v'])),
      target: (t) => {
        this.project = t.dataset['v'] ?? this.project;
        this.render();
      },
    };
    this.root.addEventListener('keydown', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.classList.contains('head-title-input')) {
        if (e.key === 'Enter') void this.saveTitle(t.value);
        if (e.key === 'Escape') {
          this.renaming = false;
          this.drawHead();
        }
      }
    });
    this.root.addEventListener('focusout', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.classList.contains('head-title-input') && this.renaming) void this.saveTitle(t.value);
    });
    // Drop a file anywhere on the conversation.
    this.root.addEventListener('dragenter', (e) => {
      if (e.dataTransfer?.types.includes('Files')) {
        this.dragDepth++;
        this.find('.drop')?.removeAttribute('hidden');
      }
    });
    this.root.addEventListener('dragleave', () => {
      if (--this.dragDepth <= 0) {
        this.dragDepth = 0;
        this.find('.drop')?.setAttribute('hidden', '');
      }
    });
    this.root.addEventListener('dragover', (e) => e.preventDefault());
    this.root.addEventListener('drop', (e) => {
      e.preventDefault();
      this.dragDepth = 0;
      this.find('.drop')?.setAttribute('hidden', '');
      const files = [...(e.dataTransfer?.files ?? [])];
      if (files.length) this.composer.stage(files);
    });
  }

  private openProjects(): Project[] {
    return this.app.store.projects.filter((p) => !p.archived);
  }

  /** Loads the conversation and its project's members, then draws. */
  async load(): Promise<void> {
    this.render();
    if (this.project) {
      this.members = (await this.app.attempt(() => this.app.store.loadMembers(this.project))) ?? [];
    }
    if (this.address) {
      this.thread = await this.app.attempt(() => this.app.store.thread(this.address as Address));
      if (!this.thread) {
        this.app.go('#');
        return;
      }
      // Opening it read it: the list's unread count goes.
      const c = this.app.store.conversation(this.address);
      if (c) c.unread = 0;
      this.app.list.drawRows();
    }
    this.render();
    this.composer.focus();
  }

  /** Reads new messages after a notice, keeping the reader where they were unless at the end. */
  async refresh(): Promise<void> {
    if (!this.thread) return;
    const tl = this.find('.timeline');
    const atEnd = !tl || tl.scrollHeight - tl.scrollTop - tl.clientHeight < 80;
    await this.app.attempt(() => (this.thread as Thread).refresh());
    this.drawHead();
    this.drawTimeline(atEnd);
  }

  protected view(): Html {
    return html`<header class="chat-head"></header>
      <div class="timeline" aria-live="polite"></div>
      <div class="composer-slot"></div>
      <div class="drop" hidden>Drop to attach</div>`;
  }

  protected override afterRender(): void {
    this.drawHead();
    this.drawTimeline(true);
    this.$('.composer-slot').replaceWith(this.composer.root);
    if (!this.composer.root.childElementCount) this.composer.render();
  }

  private drawHead(): void {
    const head = this.find('.chat-head');
    if (!head) return;
    const back = html`<button class="icon-btn back" data-act="back" aria-label="Back to conversations">${Icon.of('back')}</button>`;
    if (!this.address) {
      head.innerHTML = html`${back}<div class="head-main"><span class="head-title">New conversation</span><div class="head-sub">The title comes from your first message</div></div>`.value;
      return;
    }
    const c = this.thread?.conversation ?? this.app.store.conversation(this.address);
    const title = c?.title ?? this.address.name;
    const agents = this.members.filter((m) => m.kind === 'agent').length;
    const sub = `${this.address.project} · ${this.members.length} members${agents ? ` · ${agents} agent${agents > 1 ? 's' : ''}` : ''}${c?.closed ? ' · closed' : ''}`;
    const stack = this.members.filter((m) => m.name !== this.app.store.me?.name).slice(0, 4).map((m) => Avatar.of(m));
    const titleEl = this.renaming
      ? html`<input class="head-title-input" value="${title}" aria-label="Conversation title" maxlength="120">`
      : html`<button class="head-title" data-act="rename" title="Rename">${title}</button>`;
    head.innerHTML = html`${back}<div class="head-main">${titleEl}<div class="head-sub">${sub}</div></div><button class="members-btn" data-act="details" aria-label="Members and files"><span class="stack">${stack}</span></button>`.value;
    if (this.renaming) {
      const input = head.querySelector<HTMLInputElement>('input');
      input?.focus();
      input?.select();
    }
  }

  private async saveTitle(value: string): Promise<void> {
    if (!this.renaming || !this.address) return;
    this.renaming = false;
    const title = value.trim();
    if (title && title !== this.thread?.conversation?.title) {
      await this.app.attempt(() => this.app.store.api.patch(`/projects/${this.address?.path.replace('/', '/conversations/')}`, { title }), 'Renamed.');
      await this.refresh();
    } else {
      this.drawHead();
    }
  }

  private drawTimeline(toEnd: boolean): void {
    const tl = this.find('.timeline');
    if (!tl) return;
    if (!this.address) {
      const chips = this.openProjects().map((p) => html`<button class="chip" data-act="target" data-v="${p.name}" aria-pressed="${String(p.name === this.project)}">${p.name}</button>`);
      tl.innerHTML = html`<div class="empty"><h2>Start with a message or a file</h2><p>Pick the project it goes to. Its members, agents included, see it at once.</p><div class="targets">${chips.length ? chips : html`<span>You are in no project you can post to.</span>`}</div></div>`.value;
      return;
    }
    if (!this.thread) {
      tl.innerHTML = html`<p class="loading">Loading…</p>`.value;
      return;
    }
    const keepFrom = tl.scrollHeight - tl.scrollTop;
    tl.innerHTML = this.timeline(this.thread).value;
    Component.sizeBars(tl);
    if (toEnd) tl.scrollTop = tl.scrollHeight;
    else tl.scrollTop = tl.scrollHeight - keepFrom;
    void this.loadTails();
  }

  private timeline(t: Thread): Html {
    const me = this.app.store.me?.name;
    const text = new MessageText(new Set(this.members.map((m) => m.name)));
    const parts: Html[] = [];
    if (t.hasEarlier) parts.push(html`<button class="btn tonal earlier" data-act="earlier">Earlier messages</button>`);
    if (!t.messages.length) parts.push(html`<div class="empty"><h2>Nothing here yet</h2><p>Write the first message below.</p></div>`);
    const myLast = [...t.messages].reverse().find((m) => m.author.name === me);
    let prev: Message | undefined;
    for (const m of t.messages) {
      if (!prev || new Date(prev.at).toDateString() !== new Date(m.at).toDateString()) parts.push(html`<div class="day">${dayLabel(m.at)}</div>`);
      const first = !prev || prev.author.name !== m.author.name || new Date(m.at).getTime() - new Date(prev.at).getTime() > 5 * 60000 || new Date(prev.at).toDateString() !== new Date(m.at).toDateString();
      parts.push(this.message(m, m.author.name === me, first, text));
      if (myLast && m.id === myLast.id) parts.push(this.receipt(t, myLast));
      prev = m;
    }
    return Html.join(parts);
  }

  private message(m: Message, mine: boolean, first: boolean, text: MessageText): Html {
    const a = m.author;
    const name = `${a.display_name || a.name}${a.removed ? ' (removed)' : ''}`;
    const meta = first && !mine ? html`<div class="meta"><span class="who">${name}</span>${a.kind !== 'person' ? html`<span class="kind ${a.kind}">${a.kind}</span>` : ''}<time>${hhmm(m.at)}</time></div>` : Html.empty;
    const files = m.files ?? [];
    const images = files.filter((f) => f.state === 'stored' && SAFE_IMAGES.has(f.type));
    const imgs = images.length
      ? html`<div class="imgs ${images.length === 1 ? 'one' : ''}">${images.map((f) => html`<button data-act="img" data-v="${String(f.id)}" data-m="${String(m.id)}" aria-label="Open ${f.name}"><img src="${f.url}" alt="${f.name}" loading="lazy"></button>`)}</div>`
      : Html.empty;
    const others = files.filter((f) => !images.includes(f)).map((f) => this.file(f));
    const avatar = !mine ? (first ? Avatar.of(a) : html`<span class="spacer"></span>`) : Html.empty;
    return html`<div class="msg ${mine ? 'mine' : ''} ${first ? 'first' : ''}">${avatar}<div class="bubble">${meta}${m.text ? html`<div class="text">${text.render(m.text)}</div>` : ''}${imgs}${others}${!first || mine ? html`<span class="time-only">${hhmm(m.at)}</span>` : ''}</div></div>`;
  }

  private file(f: FileInfo): Html {
    if (f.state === 'deleted') return html`<div class="filecard gone"><span class="fc-main"><span class="fc-name">${f.name}</span><span class="fc-meta">File deleted</span></span></div>`;
    if (f.state === 'cut') return html`<div class="filecard gone"><span class="fc-main"><span class="fc-name">${f.name}</span><span class="fc-meta">Cut off: the server stopped while it arrived</span></span></div>`;
    if (isLog(f)) {
      const state = f.state === 'live' ? html`<span class="pill live">live</span>` : Html.empty;
      const tail = this.tails.get(f.id)?.lines ?? [];
      return html`<button class="logcard" data-act="log" data-v="${String(f.id)}"><div class="lc-head">${Icon.of('list')}<span class="lc-name">${f.name}</span>${state}</div><div class="lc-meta">${Bytes.format(f.size)}${f.state === 'live' ? ' so far' : ''}</div><pre class="lc-tail" data-tail="${String(f.id)}">${tail.map((l) => html`<span>${l || ' '}</span>`)}</pre></button>`;
    }
    return html`<a class="filecard" href="${f.url}" download="${f.name}"><span class="fi">${Icon.of('folder')}</span><span class="fc-main"><span class="fc-name">${f.name}</span><span class="fc-meta">${Bytes.format(f.size)}${f.state === 'live' ? ' so far, still arriving' : ''}</span></span></a>`;
  }

  /** "Read by dev-code · 14:03" under the last message of mine each reader has reached. */
  private receipt(t: Thread, mine: Message): Html {
    const me = this.app.store.me?.name;
    const readers = t.readBy.filter((r) => r.name !== me && r.up_to >= mine.id);
    if (!readers.length) return Html.empty;
    const latest = readers.reduce((a, b) => (a.at > b.at ? a : b));
    return html`<div class="receipt">${Icon.of('check')}Read by ${readers.map((r) => r.display_name || r.name).join(', ')} · ${hhmm(latest.at)}</div>`;
  }

  /** Fetches the last lines of each log card whose size changed since. */
  private async loadTails(): Promise<void> {
    const files = (this.thread?.messages ?? []).flatMap((m) => m.files ?? []).filter((f) => isLog(f) && f.state !== 'deleted' && f.state !== 'cut');
    for (const f of files) {
      if (this.tails.get(f.id)?.size === f.size) continue;
      try {
        const page = await this.app.store.api.get<{ lines: { text: string }[] }>(`/files/${f.id}/lines?tail=3`);
        this.tails.set(f.id, { size: f.size, lines: page.lines.map((l) => l.text) });
        const pre = this.find(`[data-tail="${f.id}"]`);
        if (pre) pre.innerHTML = Html.join(page.lines.map((l) => html`<span>${l.text || ' '}</span>`)).value;
      } catch {
        /* a card without its tail still opens */
      }
    }
  }

  private openImage(fileId: number, messageId: number): void {
    const m = this.thread?.messages.find((x) => x.id === messageId);
    const images = (m?.files ?? []).filter((f) => f.state === 'stored' && SAFE_IMAGES.has(f.type));
    const i = Math.max(0, images.findIndex((f) => f.id === fileId));
    if (m) this.app.layer.open(new ImageViewer(this.app, images, i, m), 'lightbox');
  }

  private openLog(fileId: number): void {
    const f = this.thread?.messages.flatMap((m) => m.files ?? []).find((x) => x.id === fileId);
    if (f) this.app.openLog(f);
  }

  /** The file a log viewer shows, as it now is: for the viewer to follow a live log. */
  fileNow(id: number): FileInfo | undefined {
    return this.thread?.messages.flatMap((m) => m.files ?? []).find((f) => f.id === id);
  }

  private async send(text: string, files: File[]): Promise<void> {
    if (!this.project) {
      Toaster.show('Pick a project first.');
      throw new Error('no project');
    }
    try {
      const address = await this.app.store.sender.send(this.project, this.address?.name ?? null, text, files);
      if (!this.address) {
        await this.app.store.loadConversations(this.project);
        this.app.go(`#c/${address}`);
        return;
      }
      await this.refresh();
      this.drawTimeline(true);
    } catch (e) {
      Toaster.show(e instanceof Error ? e.message : String(e));
      throw e;
    }
  }
}
