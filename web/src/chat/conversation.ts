import { Component } from '@shared/component';
import { Bytes } from '@shared/format';
import { Html, html } from '@shared/html';
import { i18n, n, t } from '@shared/i18n';
import { Icon } from '@shared/icons';
import { Toaster } from '@shared/toast';
import { ui } from '@shared/ui';
import { Avatar, KindName } from '@shared/widgets';
import type { ChatApp } from './app';
import { Albums, type Item } from './album';
import { pathOf } from './route';
import { Composer } from './composer';
import { look } from './look';
import { DetailsSheet, ImageViewer, MessageSheet } from './sheets';
import { Address, type Thread } from './store';
import { MessageText } from './text';
import type { FileInfo, Member, Message, Project } from './types';

const hhmm = (iso: string) => new Date(iso).toLocaleTimeString(i18n.locale, { hour: '2-digit', minute: '2-digit' });

function dayLabel(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return t('Today');
  const y = new Date(now);
  y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return t('Yesterday');
  return d.toLocaleDateString(i18n.locale, { day: 'numeric', month: 'short' });
}

// A message's text: what text.ts makes of it carries no class but
// .mention, so its code, links and mentions are styled from here. In my
// own bubble, on the primary container, they take its colour.
const TEXT =
  'text-[14.5px] whitespace-pre-wrap [overflow-wrap:anywhere] [&_code]:rounded-md3-xs [&_code]:bg-surface-high [&_code]:px-[5px] [&_code]:py-px [&_code]:font-mono [&_code]:text-[.92em] [&_a]:underline [&_a]:underline-offset-2 [&_.mention]:font-semibold';
const TEXT_THEIRS = '[&_a]:text-primary [&_.mention]:text-primary';
const TEXT_MINE = '[&_.mention]:underline [&_.mention]:underline-offset-2';

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
  /** Each bubble's photos, by its first message's id, for the viewer. */
  private readonly photoSets = new Map<number, ReturnType<typeof Albums.photos>>();
  private press: { timer: ReturnType<typeof setTimeout>; x: number; y: number } | null = null;
  /** A long press just selected: the click that ends it opens nothing. */
  private pressed = false;

  constructor(
    private readonly app: ChatApp,
    readonly address: Address | null,
    project?: string,
  ) {
    super('main', look.pane);
    this.project = address?.project ?? project ?? this.openProjects()[0]?.name ?? '';
    this.composer = new Composer({
      send: (text, files) => this.send(text, files),
      members: () => this.members.filter((m) => m.name !== this.app.store.me?.name),
      placeholder: address ? t('Message, or drop a file') : t('Write the first message, or drop a file'),
    });
    this.actions = {
      back: () => this.app.back(),
      rename: () => {
        this.renaming = true;
        this.drawHead();
      },
      details: () => {
        if (this.address) this.app.layer.open(new DetailsSheet(this.app, this.address), 'right', undefined, look.sideSheet);
      },
      earlier: async () => {
        await this.app.attempt(() => this.thread?.earlier() ?? Promise.resolve());
        this.drawTimeline(false);
      },
      img: (el) => this.openImage(Number(el.dataset['g']), Number(el.dataset['v'])),

      log: (el) => this.openLog(Number(el.dataset['v'])),
      target: (el) => {
        this.project = el.dataset['v'] ?? this.project;
        this.render();
      },
    };
    this.root.addEventListener('keydown', (e) => {
      const el = e.target as HTMLInputElement;
      if (el.classList.contains('head-title-input')) {
        if (e.key === 'Enter') void this.saveTitle(el.value);
        if (e.key === 'Escape') {
          this.renaming = false;
          this.drawHead();
        }
      }
    });
    // A long press on a message opens what can be done with it (MessageSheet),
    // as on a phone's messages app; a right click does on a desktop.
    this.root.addEventListener('pointerdown', (e) => {
      // A long press redraws the bubble under the finger, so the browser
      // sends no click to end it: the flag that swallows that click ends
      // with the next touch instead, or it would swallow that one.
      this.pressed = false;
      const el = (e.target as Element).closest<HTMLElement>('[data-ids]');
      if (!el || !e.isPrimary) return;
      this.cancelPress();
      this.press = { x: e.clientX, y: e.clientY, timer: setTimeout(() => {
        this.press = null;
        this.pressed = true;
        navigator.vibrate?.(15);
        this.openSheet(el);
      }, 450) };
    });
    this.root.addEventListener('pointermove', (e) => {
      if (this.press && Math.hypot(e.clientX - this.press.x, e.clientY - this.press.y) > 10) this.cancelPress();
    });
    for (const ev of ['pointerup', 'pointercancel', 'pointerleave'] as const) this.root.addEventListener(ev, () => this.cancelPress());
    this.root.addEventListener('contextmenu', (e) => {
      const el = (e.target as Element).closest<HTMLElement>('[data-ids]');
      if (!el) return;
      e.preventDefault();
      this.openSheet(el);
    });
    // The click that ends a long press opens nothing under the finger.
    this.root.addEventListener('click', (e) => {
      if (!this.pressed) return;
      this.pressed = false;
      e.preventDefault();
      e.stopPropagation();
    }, true);
    this.root.addEventListener('focusout', (e) => {
      const el = e.target as HTMLInputElement;
      if (el.classList.contains('head-title-input') && this.renaming) void this.saveTitle(el.value);
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
        this.app.replace(pathOf({ kind: 'list' }));
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
    return html`<header class="chat-head flex min-h-[60px] shrink-0 items-center gap-2.5 border-b border-outline-variant bg-surface py-2 pr-3 pl-2 min-[840px]:pl-5"></header>
      <div class="timeline flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-4 pt-4 pb-2 min-[840px]:px-6" aria-live="polite"></div>
      <div class="composer-slot"></div>
      <div class="drop pointer-events-none absolute inset-2 z-[6] grid place-items-center rounded-2xl border-2 border-dashed border-primary bg-primary-container/85 text-base font-bold text-on-primary-container" hidden>${t('Drop to attach')}</div>`;
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
    const back = html`<button class="${ui.iconBtn} min-[840px]:hidden" data-act="back" aria-label="${t('Back to conversations')}">${Icon.of('back')}</button>`;
    const titleClass = 'block max-w-full truncate rounded-md text-left text-base font-bold';
    const subClass = 'truncate text-[12.5px] text-on-surface-variant';
    if (!this.address) {
      head.innerHTML = html`${back}<div class="min-w-0 flex-1"><span class="${titleClass}">${t('New conversation')}</span></div>`.value;
      return;
    }
    const c = this.thread?.conversation ?? this.app.store.conversation(this.address);
    const title = c?.title ?? this.address.name;
    const agents = this.members.filter((m) => m.kind === 'agent').length;
    const sub = [
      this.address.project,
      n(this.members.length, '{n} member', '{n} members'),
      agents ? n(agents, '{n} agent', '{n} agents') : '',
      c?.closed ? t('closed') : '',
    ].filter(Boolean).join(' · ');
    const stack = this.members.filter((m) => m.name !== this.app.store.me?.name).slice(0, 4).map((m) => Avatar.of(m, 'sm'));
    const titleEl = this.renaming
      ? html`<input class="head-title-input w-full rounded-md border border-primary bg-surface-lowest px-1.5 py-0.5 text-base font-bold outline-none" value="${title}" aria-label="${t('Conversation title')}" maxlength="120">`
      : html`<button class="${titleClass} hover:bg-surface-container" data-act="rename" title="${t('Rename')}">${title}</button>`;
    head.innerHTML = html`${back}<div class="min-w-0 flex-1">${titleEl}<div class="${subClass}">${sub}</div></div><button class="flex items-center gap-1.5 rounded-full py-1 pr-2 pl-3.5 hover:bg-surface-container" data-act="details" aria-label="${t('Members and files')}"><span class="flex *:-ml-2 *:border-2 *:border-surface">${stack}</span></button>`.value;
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
      await this.app.attempt(() => this.app.store.api.patch(`/projects/${this.address?.path.replace('/', '/conversations/')}`, { title }), t('Renamed.'));
      await this.refresh();
    } else {
      this.drawHead();
    }
  }

  private drawTimeline(toEnd: boolean): void {
    const tl = this.find('.timeline');
    if (!tl) return;
    if (!this.address) {
      const chips = this.openProjects().map((p) => html`<button class="inline-flex h-8 items-center gap-1.5 rounded-md3-s border border-outline-variant px-3 text-[13px] font-semibold aria-pressed:border-transparent aria-pressed:bg-secondary-container aria-pressed:text-on-secondary-container" data-act="target" data-v="${p.name}" aria-pressed="${String(p.name === this.project)}">${p.name}</button>`);
      tl.innerHTML = html`<div class="${look.empty}"><h2>${t('Start with a message or a file')}</h2><p>${t('Pick the project it goes to.')}</p><div class="mt-1 flex flex-wrap gap-1.5">${chips.length ? chips : html`<span>${t('You are in no project you can post to.')}</span>`}</div></div>`.value;
      return;
    }
    if (!this.thread) {
      tl.innerHTML = html`<p class="${look.loading}">${t('Loading…')}</p>`.value;
      return;
    }
    const keepFrom = tl.scrollHeight - tl.scrollTop;
    tl.innerHTML = this.timeline(this.thread).value;
    Component.sizeBars(tl);
    if (toEnd) tl.scrollTop = tl.scrollHeight;
    else tl.scrollTop = tl.scrollHeight - keepFrom;
    void this.loadTails();
  }

  private timeline(th: Thread): Html {
    const me = this.app.store.me?.name;
    const text = new MessageText(new Set(this.members.map((m) => m.name)));
    const parts: Html[] = [];
    if (th.hasEarlier) parts.push(html`<button class="${ui.btn('tonal')} mt-1 mb-3 self-center" data-act="earlier">${t('Earlier messages')}</button>`);
    if (!th.messages.length) parts.push(html`<div class="${look.empty}"><h2>${t('Nothing here yet')}</h2></div>`);
    const myLast = [...th.messages].reverse().find((m) => m.author.name === me);
    this.photoSets.clear();
    let prev: Message | undefined;
    for (const item of Albums.group(th.messages)) {
      const m = item.messages[0] as Message;
      const newDay = !prev || new Date(prev.at).toDateString() !== new Date(m.at).toDateString();
      if (newDay) parts.push(html`<div class="my-2.5 self-center rounded-full bg-surface-container px-3 py-[3px] text-xs text-on-surface-variant">${dayLabel(m.at)}</div>`);
      const first = !prev || prev.author.name !== m.author.name || new Date(m.at).getTime() - new Date(prev.at).getTime() > 5 * 60000 || newDay;
      // A new author is set apart from the message above, unless a day
      // divider already is.
      parts.push(this.bubble(item, m.author.name === me, first, first && !newDay, text));
      if (myLast && item.messages.some((x) => x.id === myLast.id)) parts.push(this.receipt(th, myLast));
      prev = item.messages[item.messages.length - 1];
    }
    return Html.join(parts);
  }

  /** One bubble: a message, or an album of photos sent one message at a time. */
  private bubble(item: Item, mine: boolean, first: boolean, spaced: boolean, text: MessageText): Html {
    const m = item.messages[0] as Message;
    const last = item.messages[item.messages.length - 1] as Message;
    const a = m.author;
    const name = a.removed ? t('{name} (removed)', { name: a.display_name || a.name }) : a.display_name || a.name;
    const meta = first && !mine ? html`<div class="flex items-center gap-1.5 text-xs"><span class="font-bold">${name}</span>${a.kind !== 'person' ? html`<span class="${ui.kind[a.kind] ?? ''}">${KindName.of(a.kind)}</span>` : ''}<time class="text-on-surface-variant tabular-nums">${hhmm(m.at)}</time></div>` : Html.empty;
    const photos = Albums.photos(item);
    this.photoSets.set(m.id, photos);
    const imgs = photos.length ? this.grid(m.id, photos) : Html.empty;
    const others = item.messages.flatMap((x) => (x.deleted ? [] : (x.files ?? []).filter((f) => !photos.some((p) => p.file.id === f.id)))).map((f) => this.file(f));
    const avatar = !mine ? (first ? html`<span class="mb-0.5 flex">${Avatar.of(a, 'sm')}</span>` : html`<span class="w-7 shrink-0"></span>`) : Html.empty;
    // The first bubble of a run points at its author: a sharper corner on
    // the author's side.
    const bubble = mine
      ? `border-transparent bg-primary-container text-on-primary-container ${first ? 'rounded-br-md' : ''}`
      : `border-outline-variant bg-surface-lowest ${first ? 'rounded-bl-md' : ''}`;
    const edited = item.messages.length === 1 && m.edited ? html`<span>${t('edited')}</span> · ` : '';
    const timeOnly = html`<span class="self-end text-[11px] ${mine ? 'opacity-70' : 'text-on-surface-variant'}">${edited}${hhmm(last.at)}</span>`;
    let body: Html;
    if (m.deleted) {
      body = html`<div class="text-[14px] italic opacity-70">${t('Message deleted')}</div>`;
    } else {
      body = html`${m.text ? html`<div class="${TEXT} ${mine ? TEXT_MINE : TEXT_THEIRS}">${text.render(m.text)}</div>` : ''}${imgs}${others}`;
    }
    // On a touch screen a press is the bubble's, not the browser's text
    // selection: copying is in MessageSheet.
    return html`<div class="flex max-w-full items-end gap-2 ${mine ? 'justify-end' : ''} ${spaced ? 'mt-2.5' : ''}" data-ids="${item.messages.map((x) => x.id).join(',')}" data-mine="${String(mine)}">${avatar}<div class="relative flex max-w-[min(560px,84%)] min-w-0 flex-col gap-1.5 rounded-2xl border px-3 py-2 pointer-coarse:select-none [-webkit-touch-callout:none] ${bubble}">${meta}${body}${!first || mine ? timeOnly : ''}</div></div>`;
  }

  /**
   * Photos as a grid: one alone, two side by side, four at most, the last
   * saying how many more there are. All of them open in the viewer.
   */
  private grid(key: number, photos: ReturnType<typeof Albums.photos>): Html {
    const shown = photos.slice(0, 4);
    const more = photos.length - shown.length;
    const cols = photos.length === 1 ? 'grid-cols-1' : 'grid-cols-2';
    return html`<div class="grid w-[min(320px,100%)] gap-1 ${cols}">${shown.map((p, i) => {
      const span = photos.length === 3 && i === 0 ? 'col-span-2' : '';
      const plus = more > 0 && i === shown.length - 1
        ? html`<span class="absolute inset-0 grid place-items-center bg-black/45 text-xl font-bold text-white">+${more}</span>`
        : '';
      return html`<button class="relative block aspect-[4/3] overflow-hidden rounded-[10px] bg-surface-high ${span}" data-act="img" data-g="${String(key)}" data-v="${String(i)}" aria-label="${t('Open {name}', { name: p.file.name })}"><img class="block size-full object-cover" src="${p.file.url}" alt="${p.file.name}" loading="lazy" draggable="false">${plus}</button>`;
    })}</div>`;
  }

  /** Opens what can be done with a bubble's messages. */
  private openSheet(el: HTMLElement): void {
    const ids = new Set((el.dataset['ids'] ?? '').split(',').map(Number));
    const messages = (this.thread?.messages ?? []).filter((m) => ids.has(m.id));
    if (messages.length) MessageSheet.open(this.app, messages, el.dataset['mine'] === 'true', () => this.drawTimeline(false));
  }

  private cancelPress(): void {
    if (this.press) clearTimeout(this.press.timer);
    this.press = null;
  }

  private file(f: FileInfo): Html {
    const gone = (why: string) => html`<div class="${look.fileCard} opacity-60"><span class="${look.fileMain}"><span class="${look.fileName}">${f.name}</span><span class="${look.fileMeta}">${why}</span></span></div>`;
    if (f.state === 'deleted') return gone(t('File deleted'));
    if (f.state === 'cut') return gone(t('Cut off: the server stopped while it arrived'));
    if (isLog(f)) {
      const state = f.state === 'live' ? html`<span class="${look.live}">${t('live')}</span>` : Html.empty;
      const tail = this.tails.get(f.id)?.lines ?? [];
      return html`<button class="block w-[min(420px,100%)] overflow-hidden rounded-xl border border-outline-variant bg-surface-low text-left text-on-surface hover:border-outline" data-act="log" data-v="${String(f.id)}"><div class="flex min-w-0 items-center gap-2 px-2.5 pt-2 pb-0.5 font-semibold">${Icon.of('list')}<span class="min-w-0 flex-1 truncate">${f.name}</span>${state}</div><div class="pr-2.5 pb-2 pl-[38px] text-xs text-on-surface-variant tabular-nums">${f.state === 'live' ? t('{size} so far', { size: Bytes.format(f.size) }) : Bytes.format(f.size)}</div><pre class="m-0 overflow-hidden bg-(--log-bg) px-2.5 py-2 font-mono text-[11.5px]/[1.5] whitespace-pre text-(--log-fg) [&>span]:block [&>span]:truncate" data-tail="${String(f.id)}">${tail.map((l) => html`<span>${l || ' '}</span>`)}</pre></button>`;
    }
    return html`<a class="${look.fileCard}" href="${f.url}" download="${f.name}"><span class="flex shrink-0">${Icon.of('folder')}</span><span class="${look.fileMain}"><span class="${look.fileName}">${f.name}</span><span class="${look.fileMeta}">${f.state === 'live' ? t('{size} so far, still arriving', { size: Bytes.format(f.size) }) : Bytes.format(f.size)}</span></span></a>`;
  }

  /** "Read by dev-code · 14:03" under the last message of mine each reader has reached. */
  private receipt(th: Thread, mine: Message): Html {
    const me = this.app.store.me?.name;
    const readers = th.readBy.filter((r) => r.name !== me && r.up_to >= mine.id);
    if (!readers.length) return Html.empty;
    const latest = readers.reduce((a, b) => (a.at > b.at ? a : b));
    return html`<div class="mx-1 mt-0.5 flex items-center gap-1 self-end text-xs text-on-surface-variant [&_.ic]:size-[15px] [&_.ic]:text-success">${Icon.of('check')}${t('Read by {names} · {time}', { names: readers.map((r) => r.display_name || r.name).join(', '), time: hhmm(latest.at) })}</div>`;
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

  private openImage(key: number, index: number): void {
    const photos = this.photoSets.get(key);
    if (photos?.length) this.app.layer.open(new ImageViewer(this.app, photos, Math.min(index, photos.length - 1)), 'lightbox', undefined, 'flex h-full w-full flex-col text-white');
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
      Toaster.show(t('Pick a project first.'));
      throw new Error('no project');
    }
    try {
      const address = await this.app.store.sender.send(this.project, this.address?.name ?? null, text, files);
      if (!this.address) {
        await this.app.store.loadConversations(this.project);
        this.app.go(pathOf({ kind: 'conversation', project: address.project, name: address.name }));
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
