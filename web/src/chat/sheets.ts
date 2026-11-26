import { Refused } from '@shared/api';
import { Component } from '@shared/component';
import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { i18n, LANGS, n, t } from '@shared/i18n';
import { Icon } from '@shared/icons';
import { PickerState, PickerView } from '@shared/picker';
import { Toaster } from '@shared/toast';
import { ui } from '@shared/ui';
import { Avatar, KindName } from '@shared/widgets';
import type { ChatApp } from './app';
import { look } from './look';
import type { Address } from './store';
import type { FileInfo, LinesPage, Member, Message, Signin } from './types';

function closeButton(cls: string = ui.iconBtn): Html {
  return html`<button class="${cls}" data-act="close" aria-label="${t('Close')}">${Icon.of('close')}</button>`;
}

/** Errors as people read them. */
function reason(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// The log viewer colours lines that look like trouble, the way the mockup
// does; it decides nothing, it only helps the eye.
const ERR = /\b(error|errors|fail|failed|failure|panic|fatal|timeout|timed out|denied|exception|segfault|oops|hang)\b/i;
const WARN = /\bwarn(ing)?\b/i;
const LOG_EMPTY = 'p-4 text-(--log-dim)';

/**
 * LogViewer shows a text file a page at a time, never whole
 * (docs/design.md, "Files"): its start, its end, or what matches a search,
 * with line numbers. A live log is shown at its end and follows as it
 * grows, while Follow is on.
 */
export class LogViewer extends Component {
  private page: LinesPage | null = null;
  private mode: 'start' | 'end' | 'search' = 'end';
  private query = '';
  private follow: boolean;
  private searchTimer: ReturnType<typeof setTimeout> | undefined;

  constructor(
    private readonly app: ChatApp,
    private file: FileInfo,
  ) {
    super('div');
    this.follow = file.state === 'live';
    this.mode = file.state === 'live' ? 'end' : 'start';
    this.actions = {
      close: () => this.app.layer.close(),
      start: () => this.show('start'),
      end: () => this.show('end'),
      more: () => this.more(),
    };
    this.root.addEventListener('input', (e) => {
      const el = e.target as HTMLInputElement;
      if (el.classList.contains('log-q')) {
        this.query = el.value;
        clearTimeout(this.searchTimer);
        this.searchTimer = setTimeout(() => void this.show(this.query.trim() ? 'search' : 'start'), 250);
      }
    });
    this.root.addEventListener('change', (e) => {
      const el = e.target as HTMLInputElement;
      if (el.classList.contains('log-follow')) {
        this.follow = el.checked;
        if (this.follow) void this.show('end');
      }
    });
  }

  get fileId(): number {
    return this.file.id;
  }

  protected view(): Html {
    const f = this.file;
    return html`<div class="${look.sheetHead}"><div class="min-w-0 flex-1"><h3>${f.name}</h3><p class="log-meta">${this.meta()}</p></div>${closeButton()}</div>
      <div class="flex shrink-0 flex-wrap items-center gap-2.5 border-b border-outline-variant px-4 py-2"><label class="${look.search} h-9 flex-[1_1_200px]">${Icon.of('search')}<input class="log-q" type="search" placeholder="${t('Search in this log')}" value="${this.query}" autocomplete="off"></label>
        <span class="log-count text-[12.5px] text-on-surface-variant tabular-nums"></span>
        <button class="${ui.btn('tonal', 'sm')}" data-act="start">${t('Start')}</button><button class="${ui.btn('tonal', 'sm')}" data-act="end">${t('End')}</button>
        ${f.state === 'live' ? html`<label class="inline-flex cursor-pointer items-center gap-1.5 text-[13px] font-semibold"><input type="checkbox" class="log-follow size-4 accent-primary" ${Html.raw(this.follow ? 'checked' : '')}> ${t('Follow live')}</label>` : ''}</div>
      <div class="logbody min-h-0 flex-1 overflow-auto bg-(--log-bg) pt-2 pb-6 font-mono text-[12.5px]/[1.6] text-(--log-fg)"><p class="p-4">${t('Loading…')}</p></div>`;
  }

  private meta(): string {
    const f = this.file;
    return [
      this.page ? n(this.page.total, '{n} line', '{n} lines', { n: this.page.total.toLocaleString(i18n.locale) }) : '',
      Bytes.format(f.size),
      f.state === 'live' ? t('live') : '',
    ].filter(Boolean).join(' · ');
  }

  protected override afterRender(): void {
    void this.show(this.mode);
  }

  private url(): string {
    const base = `/files/${this.file.id}/lines`;
    if (this.mode === 'search') return `${base}?grep=${encodeURIComponent(this.query.trim())}&context=0`;
    if (this.mode === 'end') return `${base}?tail=400`;
    return `${base}?from=1`;
  }

  private async show(mode: 'start' | 'end' | 'search'): Promise<void> {
    this.mode = mode;
    if (mode !== 'end' && this.follow) {
      this.follow = false;
      const box = this.find<HTMLInputElement>('.log-follow');
      if (box) box.checked = false;
    }
    try {
      this.page = await this.app.store.api.get<LinesPage>(this.url());
    } catch (e) {
      this.$('.logbody').innerHTML = html`<p class="${LOG_EMPTY}">${reason(e)}</p>`.value;
      return;
    }
    this.draw(mode === 'end');
  }

  private async more(): Promise<void> {
    if (!this.page?.next) return;
    const base = `/files/${this.file.id}/lines`;
    const url = this.mode === 'search' ? `${base}?grep=${encodeURIComponent(this.query.trim())}&context=0&from=${this.page.next}` : `${base}?from=${this.page.next}`;
    const next = await this.app.attempt(() => this.app.store.api.get<LinesPage>(url));
    if (!next) return;
    this.page = { ...next, lines: [...this.page.lines, ...next.lines], matches: next.matches ?? this.page.matches };
    this.draw(false);
  }

  private draw(toEnd: boolean): void {
    const body = this.find('.logbody');
    if (!body || !this.page) return;
    const q = this.mode === 'search' ? this.query.trim().toLowerCase() : '';
    const rows = this.page.lines.map((l) => {
      const cls = ERR.test(l.text) ? 'text-(--log-err)' : WARN.test(l.text) ? 'text-(--log-warn)' : '';
      return html`<div class="flex pr-4 whitespace-pre"><span class="w-16 shrink-0 pr-3.5 text-right text-(--log-num) tabular-nums select-none">${String(l.n)}</span><span class="${cls}">${q ? this.highlight(l.text, q) : l.text || ' '}${l.cut ? ' …' : ''}</span></div>`;
    });
    const more = this.page.next ? html`<button class="${ui.btn('tonal', 'sm')} mx-4 my-3" data-act="more">${t('More')}</button>` : Html.empty;
    body.innerHTML = html`${rows.length ? rows : html`<p class="${LOG_EMPTY}">${this.mode === 'search' ? t('No line matches.') : t('Empty.')}</p>`}${more}`.value;
    this.$('.log-count').textContent = this.mode === 'search' ? n(this.page.matches ?? 0, '{n} matching line', '{n} matching lines') : '';
    this.$('.log-meta').textContent = this.meta();
    if (toEnd) body.scrollTop = body.scrollHeight;
  }

  private highlight(text: string, q: string): Html {
    const i = text.toLowerCase().indexOf(q);
    if (i < 0) return html`${text}`;
    return html`${text.slice(0, i)}<mark class="rounded-xs bg-(--log-warn) text-(--log-on-warn)">${text.slice(i, i + q.length)}</mark>${text.slice(i + q.length)}`;
  }

  /** Called when the conversation changed: a live log grew, or ended. */
  async update(file: FileInfo | undefined): Promise<void> {
    if (!file) return;
    const wasLive = this.file.state === 'live';
    this.file = file;
    if (this.follow || (wasLive && file.state !== 'live' && this.mode === 'end')) await this.show('end');
    else this.$('.log-meta').textContent = this.meta();
  }
}

const LB_NAV = 'absolute top-1/2 grid size-11 -translate-y-1/2 place-items-center rounded-full bg-white/12 text-white';

/**
 * MessageSheet is what a long press on a message opens on a phone, as
 * Discord's does: the message, then Copy, and Edit and Delete for one's own.
 * Edit hands back to the conversation, which edits in the bubble; Delete asks
 * in the sheet. On a desktop the hover toolbar's Delete opens it straight on
 * that question, as a small dialog.
 */
export class MessageSheet extends Component {
  private busy = false;

  constructor(
    private readonly app: ChatApp,
    /** The bubble's messages: one, or an album's. */
    private readonly messages: Message[],
    private readonly mine: boolean,
    private readonly hooks: { changed: () => void; edit: (id: number) => void },
    private mode: 'menu' | 'delete' = 'menu',
  ) {
    super('div');
    this.actions = {
      close: () => this.app.layer.close(),
      copy: () => void this.copy(),
      edit: () => {
        const m = this.one;
        this.app.layer.close();
        if (m) this.hooks.edit(m.id);
      },
      delete: () => {
        this.mode = 'delete';
        this.render();
      },
      // Cancel on the question goes back to the menu, or closes when the
      // hover toolbar opened the question alone.
      back: () => {
        if (this.started === 'delete') return this.app.layer.close();
        this.mode = 'menu';
        this.render();
      },
      'delete-yes': () => void this.remove(),
    };
    this.started = mode;
  }

  private readonly started: 'menu' | 'delete';

  /** Opens it the way the screen suits: from the bottom on a phone, a dialog on a desktop. */
  static open(app: ChatApp, messages: Message[], mine: boolean, hooks: { changed: () => void; edit: (id: number) => void }, mode: 'menu' | 'delete' = 'menu'): void {
    const wide = matchMedia('(min-width: 640px)').matches;
    app.layer.open(new MessageSheet(app, messages, mine, hooks, mode), wide ? 'center' : 'bottom', undefined,
      wide ? 'flex w-full max-w-[440px] flex-col overflow-hidden rounded-md3-xl bg-surface [animation:pop_200ms_cubic-bezier(.2,0,0,1)]'
        : 'flex max-h-[90%] w-full flex-col rounded-t-md3-xl bg-surface pb-[env(safe-area-inset-bottom,0px)]');
  }

  /** Deletes messages, saying so; how many were deleted. */
  static async deleteAll(app: ChatApp, messages: Message[]): Promise<number> {
    let done = 0;
    for (const m of messages) {
      const r = await app.attempt(() => app.store.api.delete(`/messages/${m.id}`));
      if (r === null) break;
      Object.assign(m, { deleted: true, text: undefined, files: [] });
      done++;
    }
    if (done) Toaster.show(n(done, 'Message deleted.', '{n} messages deleted.'));
    return done;
  }

  private get one(): Message | undefined {
    return this.messages.length === 1 ? this.messages[0] : undefined;
  }

  protected view(): Html {
    const m = this.messages[0];
    if (!m) return Html.empty;
    const files = this.messages.reduce((k, x) => k + (x.files?.length ?? 0), 0);
    const preview = this.one?.text
      ? html`<p class="line-clamp-3 text-[14px] whitespace-pre-wrap text-on-surface">${this.one.text}</p>`
      : html`<p class="text-[14px] text-on-surface-variant">${n(files, '{n} file', '{n} files')}</p>`;
    const who = html`<div class="text-xs font-semibold text-on-surface-variant">${m.author.display_name || m.author.name}</div>`;
    if (this.mode === 'delete') {
      // Discord's question: the title, the message as it is, and the button.
      return html`<div class="flex flex-col gap-3 p-5">
        <h2 class="text-lg font-semibold">${n(this.messages.length, 'Delete message', 'Delete {n} messages')}</h2>
        <div class="flex flex-col gap-1 rounded-xl bg-surface-container px-3.5 py-2.5">${who}${preview}</div>
        <div class="flex justify-end gap-2"><button class="${ui.btn('text')}" data-act="back">${t('Cancel')}</button><button class="${ui.btn('danger')}" data-act="delete-yes" ${Html.raw(this.busy ? 'disabled' : '')}>${t('Delete')}</button></div></div>`;
    }
    const head = html`<div class="flex items-start gap-3 border-b border-outline-variant/60 px-5 pt-4 pb-3"><div class="flex min-w-0 flex-1 flex-col gap-1">${who}${preview}</div>${closeButton()}</div>`;
    const ROW = 'state-layer flex min-h-12 w-full items-center gap-4 px-5 text-left text-[15px] [&_.ic]:size-5 [&_.ic]:text-on-surface-variant';
    const rows: Html[] = [];
    if (this.mine && this.one && !this.one.deleted && this.one.text !== undefined) rows.push(html`<button class="${ROW}" data-act="edit">${Icon.of('pencil')}${t('Edit')}</button>`);
    if (this.one?.text) rows.push(html`<button class="${ROW}" data-act="copy">${Icon.of('copy')}${t('Copy')}</button>`);
    if (this.mine && this.messages.every((x) => !x.deleted)) rows.push(html`<button class="${ROW} text-error [&_.ic]:!text-error" data-act="delete">${Icon.of('trash')}${t('Delete')}</button>`);
    if (!rows.length) rows.push(html`<p class="px-5 py-3 text-sm text-on-surface-variant">${t('Only your own messages can be changed or deleted')}</p>`);
    return html`${head}<div class="flex flex-col py-2">${rows}</div>`;
  }

  private async copy(): Promise<void> {
    try {
      await navigator.clipboard.writeText(this.one?.text ?? '');
      Toaster.show(t('Copied.'));
    } catch {
      Toaster.show(t('Select the text to copy it.'));
    }
    this.app.layer.close();
  }

  private async remove(): Promise<void> {
    if (this.busy) return;
    this.busy = true;
    this.render();
    await MessageSheet.deleteAll(this.app, this.messages);
    this.busy = false;
    this.app.layer.close();
    this.hooks.changed();
  }
}

/**
 * ImageViewer shows photos full screen, one at a time: a message's, or an
 * album's, the run of photos an agent sends one message at a time
 * (album.ts). A swipe, the arrows or the keys go from one to the next, and
 * a strip of them along the bottom shows the whole set at once and opens
 * any of it.
 */
export class ImageViewer extends Component {
  private swipeFrom: { x: number; y: number } | null = null;

  constructor(
    private readonly app: ChatApp,
    private readonly photos: { file: FileInfo; message: Message }[],
    private index: number,
  ) {
    super('div');
    this.actions = {
      close: () => this.app.layer.close(),
      prev: () => this.step(-1),
      next: () => this.step(1),
      goto: (el) => this.show(Number(el.dataset['v'])),
    };
    this.root.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowLeft') this.step(-1);
      if (e.key === 'ArrowRight') this.step(1);
    });
    // A swipe: mostly sideways and long enough not to be a tap. Pinching
    // to zoom moves two fingers and is left to the browser.
    this.root.addEventListener('pointerdown', (e) => {
      if ((e.target as Element).closest('.lb-stage') && e.isPrimary) this.swipeFrom = { x: e.clientX, y: e.clientY };
    });
    this.root.addEventListener('pointerup', (e) => {
      const from = this.swipeFrom;
      this.swipeFrom = null;
      if (!from) return;
      const dx = e.clientX - from.x;
      const dy = e.clientY - from.y;
      if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5) this.step(dx < 0 ? 1 : -1);
    });
    this.root.addEventListener('pointercancel', () => (this.swipeFrom = null));
    this.root.tabIndex = -1;
  }

  private step(d: number): void {
    this.show((this.index + d + this.photos.length) % this.photos.length);
  }

  private show(i: number): void {
    if (i < 0 || i >= this.photos.length || i === this.index) return;
    this.index = i;
    this.render();
  }

  protected view(): Html {
    const p = this.photos[this.index];
    if (!p) return Html.empty;
    const { file: f, message: m } = p;
    const by = `${m.author.display_name || m.author.name} · ${new Date(m.at).toLocaleString(i18n.locale, { dateStyle: 'short', timeStyle: 'short' })}`;
    const many = this.photos.length > 1;
    const nav = many
      ? html`<button class="${LB_NAV} left-[max(12px,env(safe-area-inset-left,0px))] max-[700px]:hidden" data-act="prev" aria-label="${t('Previous')}">${Icon.of('back')}</button><button class="${LB_NAV} right-[max(12px,env(safe-area-inset-right,0px))] max-[700px]:hidden" data-act="next" aria-label="${t('Next')}">${Icon.of('chev')}</button>`
      : Html.empty;
    const strip = many
      ? html`<div class="lb-strip flex shrink-0 justify-center gap-1.5 overflow-x-auto px-4 pt-1 pb-[calc(12px+env(safe-area-inset-bottom,0px))] [scrollbar-width:none]">${this.photos.map((x, i) =>
          html`<button class="size-14 shrink-0 overflow-hidden rounded-md ${i === this.index ? 'ring-2 ring-white' : 'opacity-55'}" data-act="goto" data-v="${String(i)}" aria-label="${t('{i} of {total}', { i: i + 1, total: this.photos.length })}" ${Html.raw(i === this.index ? 'aria-current="true"' : '')}><img class="size-full object-cover" src="${x.file.url}" alt="" loading="lazy"></button>`)}</div>`
      : Html.empty;
    return html`<div class="flex items-center gap-2.5 pt-[calc(10px+env(safe-area-inset-top,0px))] pr-3 pb-2.5 pl-4"><div class="min-w-0 flex-1 text-[13px]"><b class="block truncate text-sm">${f.name}</b>${by}${many ? ` · ${t('{i} of {total}', { i: this.index + 1, total: this.photos.length })}` : ''}</div>
      <a class="${look.iconBtnDark}" href="${f.url}" download="${f.name}" aria-label="${t('Download')}">${Icon.of('folder')}</a>${closeButton(look.iconBtnDark)}</div>
      <div class="lb-stage relative grid min-h-0 flex-1 touch-pan-y place-items-center px-4 pb-3 select-none"><img class="max-h-full max-w-full rounded-md object-contain" src="${f.url}" alt="${f.name}" draggable="false">${nav}</div>${strip}`;
  }

  protected override afterRender(): void {
    this.root.focus();
    this.find('.lb-strip [aria-current]')?.scrollIntoView({ block: 'nearest', inline: 'center' });
  }
}

/**
 * DetailsSheet is a conversation's members, files and address, and where it
 * is closed or reopened. People add members here, from everyone who
 * exists, through the same picker as the admin UI.
 */
export class DetailsSheet extends Component {
  private members: Member[] = [];

  constructor(
    private readonly app: ChatApp,
    private readonly address: Address,
  ) {
    super('div');
    this.actions = {
      close: () => this.app.layer.close(),
      copy: async (el) => {
        try {
          await navigator.clipboard.writeText(el.dataset['v'] ?? '');
          Toaster.show(t('Copied.'));
        } catch {
          Toaster.show(t('Select the text to copy it.'));
        }
      },
      add: () => this.openPicker(),
      'toggle-closed': async () => {
        const closed = !this.app.store.conversation(this.address)?.closed;
        await this.app.attempt(() => this.app.store.api.patch(`/projects/${this.address.path.replace('/', '/conversations/')}`, { closed }),
          closed ? t('Conversation closed.') : t('Conversation reopened.'));
        await this.app.refreshProject(this.address.project);
        this.render();
      },
      log: (el) => {
        const f = this.files().find((x) => x.id === Number(el.dataset['v']));
        if (f) this.app.openLog(f);
      },
    };
  }

  private files(): FileInfo[] {
    const th = this.app.store.threads.get(this.address.toString());
    return (th?.messages ?? []).flatMap((m) => m.files ?? []).reverse();
  }

  protected view(): Html {
    const c = this.app.store.conversation(this.address);
    const me = this.app.store.me;
    const kindText = (m: Member): string => {
      const description = m.description ?? '';
      if (m.kind === 'person') return t('Person');
      if (m.kind === 'agent') return description ? t('Agent · {description}', { description }) : t('Agent');
      return description ? t('Device · sends only · {description}', { description }) : t('Device · sends only');
    };
    const members = this.members.map((m) => html`<div class="${look.mem}">${Avatar.of(m)}<div class="min-w-0 flex-1"><div class="${look.memName}">${m.display_name || m.name}${m.kind !== 'person' ? html`<span class="${ui.kind[m.kind] ?? ''}">${KindName.of(m.kind)}</span>` : ''}</div><div class="${look.memSub}">${m.name === me?.name ? t('You') : kindText(m)}</div></div></div>`);
    const files = this.files().filter((f) => f.state !== 'deleted' && f.state !== 'cut').map((f) => {
      const inner = html`<span class="grid size-10 shrink-0 place-items-center rounded-md3-s bg-surface-high">${Icon.of(f.lines_url ? 'list' : 'folder')}</span><span class="${look.fileMain}"><span class="${look.fileName}">${f.name}</span><span class="${look.fileMeta}">${Bytes.format(f.size)}${f.state === 'live' ? ` · ${t('live')}` : ''}</span></span>`;
      const row = 'flex w-full items-center gap-2.5 px-4 py-2 text-left text-inherit no-underline hover:bg-surface-container';
      // A log opens in the viewer; anything else is a download.
      return f.lines_url
        ? html`<button class="${row}" data-act="log" data-v="${String(f.id)}">${inner}</button>`
        : html`<a class="${row}" href="${f.url}" download="${f.name}">${inner}</a>`;
    });
    const [sendBefore, sendAfter] = t('A device sends here with {command}').split('{command}');
    const send = `curl -T boot.log -H "Authorization: Bearer cfd_…" ${location.origin}/api/v1/send/${this.address}/`;
    return html`<div class="${look.sheetHead}"><div class="min-w-0 flex-1"><h3>${c?.title ?? this.address.name}</h3><p>${c?.closed ? t('{project} · closed', { project: this.address.project }) : this.address.project}</p></div>${closeButton()}</div>
      <div class="${look.sheetScroll}">
        <div class="${look.sec}">${t('Address for agents and scripts')}</div>
        <div class="mx-4 mt-1 flex flex-col gap-1.5 rounded-xl bg-surface-container px-3 py-2.5 text-[12.5px] [&_code]:font-mono [&_code]:[overflow-wrap:anywhere]"><div class="flex items-center gap-2"><code class="min-w-0 flex-1">${this.address.toString()}</code><button class="rounded-md px-2 py-1 font-semibold text-primary" data-act="copy" data-v="${this.address.toString()}">${t('Copy')}</button></div>
          <p class="text-on-surface-variant">${sendBefore}<code>${send}</code>${sendAfter}</p></div>
        <div class="${look.sec}">${t('Members · {n}', { n: this.members.length })}</div>${members}
        ${me?.kind === 'person' ? html`<div class="${look.pad}"><button class="${ui.btn('tonal')}" data-act="add">${t('Add or remove people and agents')}</button></div>` : ''}
        <div class="${look.sec}">${t('Files')}</div>${files.length ? files : html`<p class="${look.note}">${t('No files yet.')}</p>`}
        <div class="${look.pad}"><button class="${ui.btn('tonal')}" data-act="toggle-closed">${c?.closed ? t('Reopen conversation') : t('Close conversation')}</button></div>
      </div>`;
  }

  protected override afterRender(): void {
    if (!this.members.length) {
      void this.app.attempt(() => this.app.store.loadMembers(this.address.project)).then((ms) => {
        if (ms && ms.length) {
          this.members = ms;
          this.render();
        }
      });
    }
  }

  private async openPicker(): Promise<void> {
    const dir = await this.app.attempt(() => this.app.store.api.get<{ members: Member[] }>('/directory'));
    if (!dir) return;
    const current = this.members.filter((m) => m.kind !== 'device').map((m) => m.name);
    const state = new PickerState(dir.members.map((m) => ({
      id: m.name, label: m.display_name || m.name, kind: m.kind, avatar: Avatar.of(m, 'sm'),
      text: [m.name, m.display_name, m.description].join(' ').toLowerCase(),
      sub: m.kind === 'person' ? `@${m.name}` : m.description || KindName.of(m.kind),
    })), current, current);
    this.app.layer2.open(new MembersDialog(this.app, this.address.project, state, async () => {
      this.members = await this.app.store.loadMembers(this.address.project);
      this.render();
    }), 'center');
  }
}

/** MembersDialog is the picker over everyone, for one project's members. */
class MembersDialog extends Component {
  private readonly picker: PickerView;

  constructor(
    private readonly app: ChatApp,
    private readonly project: string,
    private readonly state: PickerState,
    private readonly saved: () => Promise<void>,
  ) {
    super('div');
    this.picker = new PickerView(state, { kinds: true, inline: false, placeholder: t('Search people and agents'), onChange: () => this.updateSave() });
    this.actions = {
      close: () => this.app.layer2.close(),
      save: () => this.save(),
    };
  }

  protected view(): Html {
    return html`<div class="${ui.dialogHead}"><div class="min-w-0 flex-1"><h2>${t('Members of {project}', { project: this.project })}</h2></div>${closeButton()}</div>
      <div class="${ui.dialogBody}"><div class="pk-slot"></div></div>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('tonal')}" data-act="close">${t('Cancel')}</button><button class="${ui.btn('filled')} save" data-act="save" disabled>${t('Save')}</button></div>`;
  }

  protected override afterRender(): void {
    this.$('.pk-slot').replaceWith(this.picker.root);
    this.picker.render();
    this.picker.focus();
  }

  private updateSave(): void {
    const btn = this.find<HTMLButtonElement>('.save');
    if (!btn) return;
    const { add, remove } = this.state.changes();
    btn.textContent = this.state.saveLabel();
    btn.disabled = !add.length && !remove.length;
  }

  private async save(): Promise<void> {
    const { add, remove } = this.state.changes();
    const p = encodeURIComponent(this.project);
    this.app.layer2.close();
    await this.app.attempt(async () => {
      for (const name of add) await this.app.store.api.post(`/projects/${p}/members`, { name });
      for (const name of remove) await this.app.store.api.delete(`/projects/${p}/members/${encodeURIComponent(name)}`);
    }, t('Saved: {added} added, {removed} removed.', { added: add.length, removed: remove.length }));
    await this.saved();
  }
}

/**
 * ProfileSheet is the one place in the chat app with settings, and they are
 * the person's own: their sign-ins, signing in another phone, the language
 * (this browser's, as i18n keeps it), signing out (docs/design.md, "The
 * chat app", rule 8).
 */
export class ProfileSheet extends Component {
  private signins: Signin[] | null = null;
  private phone: { url: string; qr?: string; expires_at: string } | null = null;
  private error = '';

  constructor(private readonly app: ChatApp) {
    super('div');
    this.actions = {
      close: () => this.app.layer.close(),
      'sign-out-one': async (el) => {
        await this.app.attempt(() => this.app.store.api.delete(`/me/signins/${el.dataset['v']}`), t('Signed out.'));
        await this.loadSignins();
      },
      'sign-out': async () => {
        await this.app.attempt(() => this.app.store.signOut());
        this.app.showSignIn(t('You are signed out.'));
      },
      'phone-code': () => this.showPhoneCode(),
      lang: (el) => {
        const lang = LANGS.find((l) => l.id === el.dataset['v']);
        if (lang && lang.id !== i18n.lang) i18n.choose(lang.id);
      },
    };
  }

  protected view(): Html {
    const me = this.app.store.me;
    const signins = this.signins === null ? html`<p class="${look.note}">${t('Loading…')}</p>`
      : this.signins.map((s) => html`<div class="${look.mem}"><div class="min-w-0 flex-1"><div class="${look.memName}">${s.current ? t('{what} · this one', { what: s.what }) : s.what}</div><div class="${look.memSub}">${s.surface === 'admin' ? t('admin UI') : t('chat app')} · ${s.from} · ${time.ago(s.last_used)}</div></div>${s.current ? '' : html`<button class="${ui.btn('tonal', 'sm')}" data-act="sign-out-one" data-v="${String(s.id)}">${t('Sign out')}</button>`}</div>`);
    // The QR code stays black on white in both themes, for the camera.
    const phone = this.phone
      ? html`<div class="flex flex-wrap items-center gap-3.5 px-4 pt-2 pb-3"><div class="w-40 max-w-full shrink-0 rounded-xl bg-white p-2">${this.phone.qr ? html`<img class="block w-full [image-rendering:pixelated]" src="${this.phone.qr}" alt="${t('QR code that signs in a phone')}">` : ''}</div><p class="flex-[1_1_180px] text-[13px] text-on-surface-variant">${t("Scan it with the phone's camera. It works once, {expires}, and signs that phone in as you.", { expires: time.until(this.phone.expires_at) })}</p></div>`
      : html`<div class="${look.pad}"><button class="${ui.btn('filled')}" data-act="phone-code">${t('Show a code')}</button>
          ${this.error ? html`<p class="${ui.error} mt-2">${this.error}</p>` : ''}</div>`;
    return html`<div class="${look.sheetHead}">${me ? Avatar.of(me) : ''}<div class="min-w-0 flex-1"><h3>${me?.display_name || me?.name || ''}</h3><p>${me?.admin ? t('@{name} · admin', { name: me.name }) : `@${me?.name ?? ''}`}</p></div>${closeButton()}</div>
      <div class="${look.sheetScroll}">
        <div class="${look.sec}">${t('Sign in another phone')}</div>${phone}
        <div class="${look.sec}">${t('Signed in')}</div>${signins}
        <div class="flex items-center gap-3 px-4 pt-3 pb-1"><span class="flex-1 font-semibold">${t('Language')}</span><div class="${ui.seg}" role="group" aria-label="${t('Language')}">${LANGS.map((l) => html`<button type="button" class="${ui.segBtn}" lang="${l.id}" aria-pressed="${String(l.id === i18n.lang)}" data-act="lang" data-v="${l.id}">${l.name}</button>`)}</div></div>
        <div class="${look.pad}"><button class="${ui.btn('tonal')}" data-act="sign-out">${t('Sign out of this browser')}</button></div>
      </div>`;
  }

  protected override afterRender(): void {
    if (this.signins === null) void this.loadSignins();
  }

  private async loadSignins(): Promise<void> {
    const r = await this.app.attempt(() => this.app.store.api.get<{ signins: Signin[] }>('/me/signins'));
    this.signins = r?.signins ?? [];
    this.render();
  }

  private async showPhoneCode(): Promise<void> {
    try {
      this.phone = await this.app.store.api.post<{ url: string; qr?: string; expires_at: string }>('/me/phone');
      this.error = '';
    } catch (e) {
      this.error = e instanceof Refused ? e.message : reason(e);
    }
    this.render();
    if (this.phone) this.watchPhone(this.phone.expires_at);
  }

  /**
   * Watches for the phone that scans the code: once a sign-in more than
   * there were appears, the code is used and goes, and the phone is in the
   * list below. The code goes at its expiry too, since it no longer works.
   */
  private watchPhone(expires: string): void {
    const before = this.signins?.length ?? 0;
    const timer = setInterval(async () => {
      if (!this.root.isConnected || !this.phone) return clearInterval(timer);
      if (Date.now() > Date.parse(expires)) {
        clearInterval(timer);
        this.phone = null;
        this.render();
        return;
      }
      const r = await this.app.store.api.get<{ signins: Signin[] }>('/me/signins').catch(() => null);
      if (!r || r.signins.length <= before) return;
      clearInterval(timer);
      this.phone = null;
      this.signins = r.signins;
      this.render();
      Toaster.show(t('The phone is signed in.'));
    }, 2500);
  }
}

/**
 * SignInView is what a browser not signed in sees. People have no
 * passwords: a browser is signed in by opening a sign-in link an admin made,
 * or by scanning the code a phone already signed in shows (profile, "Sign in
 * another phone"). So there is nothing to type here.
 */
export class SignInView extends Component {
  constructor(
    _app: ChatApp,
    private readonly message?: string,
  ) {
    super('div', 'grid min-h-full place-items-center px-[max(16px,env(safe-area-inset-left,0px))] py-6');
  }

  protected view(): Html {
    const [before, after] = t('Open the sign-in link an admin sent you, or scan the code from a phone that is signed in: {where}.').split('{where}');
    return html`<div class="${ui.card} w-[min(380px,100%)] gap-4 p-6">
      <div class="flex items-center gap-2 text-xl font-bold tracking-[-.01em] [&_svg]:size-[26px] [&_svg]:text-primary">${Icon.logo} cafard</div>
      ${this.message ? html`<p class="${ui.error}" role="alert">${this.message}</p>` : ''}
      <p class="text-sm">${before}<span class="text-on-surface-variant">${t('profile, Sign in another phone')}</span>${after}</p></div>`;
  }

  focus(): void {}
}
