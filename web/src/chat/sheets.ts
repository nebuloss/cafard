import { Refused } from '@shared/api';
import { Component } from '@shared/component';
import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { PickerState, PickerView } from '@shared/picker';
import { Toaster } from '@shared/toast';
import { ui } from '@shared/ui';
import { Avatar } from '@shared/widgets';
import type { ChatApp } from './app';
import { look } from './look';
import type { Address } from './store';
import type { FileInfo, LinesPage, Member, Message, Signin } from './types';

function closeButton(cls: string = ui.iconBtn): Html {
  return html`<button class="${cls}" data-act="close" aria-label="Close">${Icon.of('close')}</button>`;
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
      const t = e.target as HTMLInputElement;
      if (t.classList.contains('log-q')) {
        this.query = t.value;
        clearTimeout(this.searchTimer);
        this.searchTimer = setTimeout(() => void this.show(this.query.trim() ? 'search' : 'start'), 250);
      }
    });
    this.root.addEventListener('change', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.classList.contains('log-follow')) {
        this.follow = t.checked;
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
      <div class="flex shrink-0 flex-wrap items-center gap-2.5 border-b border-outline-variant px-4 py-2"><label class="${look.search} h-9 flex-[1_1_200px]">${Icon.of('search')}<input class="log-q" type="search" placeholder="Search in this log" value="${this.query}" autocomplete="off"></label>
        <span class="log-count text-[12.5px] text-on-surface-variant tabular-nums"></span>
        <button class="${ui.btn('tonal', 'sm')}" data-act="start">Start</button><button class="${ui.btn('tonal', 'sm')}" data-act="end">End</button>
        ${f.state === 'live' ? html`<label class="inline-flex cursor-pointer items-center gap-1.5 text-[13px] font-semibold"><input type="checkbox" class="log-follow size-4 accent-primary" ${Html.raw(this.follow ? 'checked' : '')}> Follow live</label>` : ''}</div>
      <div class="logbody min-h-0 flex-1 overflow-auto bg-(--log-bg) pt-2 pb-6 font-mono text-[12.5px]/[1.6] text-(--log-fg)"><p class="p-4">Loading…</p></div>`;
  }

  private meta(): string {
    const f = this.file;
    const total = this.page ? `${this.page.total.toLocaleString()} lines · ` : '';
    return `${total}${Bytes.format(f.size)}${f.state === 'live' ? ' · live' : ''}`;
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
    const more = this.page.next ? html`<button class="${ui.btn('tonal', 'sm')} mx-4 my-3" data-act="more">More</button>` : Html.empty;
    body.innerHTML = html`${rows.length ? rows : html`<p class="${LOG_EMPTY}">${this.mode === 'search' ? 'No line matches.' : 'Empty.'}</p>`}${more}`.value;
    this.$('.log-count').textContent = this.mode === 'search' ? `${this.page.matches ?? 0} matching line${this.page.matches === 1 ? '' : 's'}` : '';
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

/** ImageViewer shows a message's photos full screen, one at a time. */
export class ImageViewer extends Component {
  constructor(
    private readonly app: ChatApp,
    private readonly images: FileInfo[],
    private index: number,
    private readonly message: Message,
  ) {
    super('div');
    this.actions = {
      close: () => this.app.layer.close(),
      prev: () => this.step(-1),
      next: () => this.step(1),
    };
    this.root.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowLeft') this.step(-1);
      if (e.key === 'ArrowRight') this.step(1);
    });
    this.root.tabIndex = -1;
  }

  private step(d: number): void {
    this.index = (this.index + d + this.images.length) % this.images.length;
    this.render();
  }

  protected view(): Html {
    const f = this.images[this.index];
    if (!f) return Html.empty;
    const by = `${this.message.author.display_name || this.message.author.name} · ${new Date(this.message.at).toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' })}`;
    const nav = this.images.length > 1
      ? html`<button class="${LB_NAV} left-[max(12px,env(safe-area-inset-left,0px))]" data-act="prev" aria-label="Previous">${Icon.of('back')}</button><button class="${LB_NAV} right-[max(12px,env(safe-area-inset-right,0px))]" data-act="next" aria-label="Next">${Icon.of('chev')}</button>`
      : Html.empty;
    return html`<div class="flex items-center gap-2.5 pt-[calc(10px+env(safe-area-inset-top,0px))] pr-3 pb-2.5 pl-4"><div class="min-w-0 flex-1 text-[13px]"><b class="block text-sm">${f.name}</b>${by}${this.images.length > 1 ? ` · ${this.index + 1} of ${this.images.length}` : ''}</div>
      <a class="${look.iconBtnDark}" href="${f.url}" download="${f.name}" aria-label="Download">${Icon.of('folder')}</a>${closeButton(look.iconBtnDark)}</div>
      <div class="relative grid min-h-0 flex-1 place-items-center px-4 pb-6"><img class="max-h-full max-w-full rounded-md object-contain" src="${f.url}" alt="${f.name}">${nav}</div>`;
  }

  protected override afterRender(): void {
    this.root.focus();
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
      copy: async (t) => {
        try {
          await navigator.clipboard.writeText(t.dataset['v'] ?? '');
          Toaster.show('Copied.');
        } catch {
          Toaster.show('Select the text to copy it.');
        }
      },
      add: () => this.openPicker(),
      'toggle-closed': async () => {
        const closed = !this.app.store.conversation(this.address)?.closed;
        await this.app.attempt(() => this.app.store.api.patch(`/projects/${this.address.path.replace('/', '/conversations/')}`, { closed }),
          closed ? 'Conversation closed.' : 'Conversation reopened.');
        await this.app.refreshProject(this.address.project);
        this.render();
      },
      log: (t) => {
        const f = this.files().find((x) => x.id === Number(t.dataset['v']));
        if (f) this.app.openLog(f);
      },
    };
  }

  private files(): FileInfo[] {
    const t = this.app.store.threads.get(this.address.toString());
    return (t?.messages ?? []).flatMap((m) => m.files ?? []).reverse();
  }

  protected view(): Html {
    const c = this.app.store.conversation(this.address);
    const me = this.app.store.me;
    const kindText = (m: Member) => m.kind === 'person' ? 'Person' : m.kind === 'agent' ? `Agent${m.description ? ' · ' + m.description : ''}` : `Device · sends only${m.description ? ' · ' + m.description : ''}`;
    const members = this.members.map((m) => html`<div class="${look.mem}">${Avatar.of(m)}<div class="min-w-0 flex-1"><div class="${look.memName}">${m.display_name || m.name}${m.kind !== 'person' ? html`<span class="${ui.kind[m.kind] ?? ''}">${m.kind}</span>` : ''}</div><div class="${look.memSub}">${m.name === me?.name ? 'You' : kindText(m)}</div></div></div>`);
    const files = this.files().filter((f) => f.state !== 'deleted' && f.state !== 'cut').map((f) => {
      const inner = html`<span class="grid size-10 shrink-0 place-items-center rounded-md3-s bg-surface-high">${Icon.of(f.lines_url ? 'list' : 'folder')}</span><span class="${look.fileMain}"><span class="${look.fileName}">${f.name}</span><span class="${look.fileMeta}">${Bytes.format(f.size)}${f.state === 'live' ? ' · live' : ''}</span></span>`;
      const row = 'flex w-full items-center gap-2.5 px-4 py-2 text-left text-inherit no-underline hover:bg-surface-container';
      // A log opens in the viewer; anything else is a download.
      return f.lines_url
        ? html`<button class="${row}" data-act="log" data-v="${String(f.id)}">${inner}</button>`
        : html`<a class="${row}" href="${f.url}" download="${f.name}">${inner}</a>`;
    });
    const send = `curl -T boot.log -H "Authorization: Bearer cfd_…" ${location.origin}/api/v1/send/${this.address}/`;
    return html`<div class="${look.sheetHead}"><div class="min-w-0 flex-1"><h3>${c?.title ?? this.address.name}</h3><p>${this.address.project}${c?.closed ? ' · closed' : ''}</p></div>${closeButton()}</div>
      <div class="${look.sheetScroll}">
        <div class="${look.sec}">Address for agents and scripts</div>
        <div class="mx-4 mt-1 flex flex-col gap-1.5 rounded-xl bg-surface-container px-3 py-2.5 text-[12.5px] [&_code]:font-mono [&_code]:[overflow-wrap:anywhere]"><div class="flex items-center gap-2"><code class="min-w-0 flex-1">${this.address.toString()}</code><button class="rounded-md px-2 py-1 font-semibold text-primary" data-act="copy" data-v="${this.address.toString()}">Copy</button></div>
          <p class="text-on-surface-variant">A device sends here with <code>${send}</code></p></div>
        <div class="${look.sec}">Members · ${this.members.length}</div>${members}
        ${me?.kind === 'person' ? html`<div class="${look.pad}"><button class="${ui.btn('tonal')}" data-act="add">Add or remove people and agents</button></div>` : ''}
        <div class="${look.sec}">Files</div>${files.length ? files : html`<p class="${look.note}">No files yet.</p>`}
        <div class="${look.pad}"><button class="${ui.btn('tonal')}" data-act="toggle-closed">${c?.closed ? 'Reopen conversation' : 'Close conversation'}</button></div>
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
      sub: m.kind === 'person' ? `@${m.name}` : m.description || 'agent',
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
    this.picker = new PickerView(state, { kinds: true, inline: false, placeholder: 'Search people and agents', onChange: () => this.updateSave() });
    this.actions = {
      close: () => this.app.layer2.close(),
      save: () => this.save(),
    };
  }

  protected view(): Html {
    return html`<div class="${ui.dialogHead}"><div class="min-w-0 flex-1"><h2>Members of ${this.project}</h2></div>${closeButton()}</div>
      <div class="${ui.dialogBody}"><div class="pk-slot"></div></div>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('tonal')}" data-act="close">Cancel</button><button class="${ui.btn('filled')} save" data-act="save" disabled>Save</button></div>`;
  }

  protected override afterRender(): void {
    this.$('.pk-slot').replaceWith(this.picker.root);
    this.picker.render();
    this.picker.focus();
  }

  private updateSave(): void {
    const btn = this.find<HTMLButtonElement>('.save');
    if (!btn) return;
    btn.textContent = this.state.saveLabel();
    btn.disabled = btn.textContent === 'Save';
  }

  private async save(): Promise<void> {
    const { add, remove } = this.state.changes();
    const p = encodeURIComponent(this.project);
    this.app.layer2.close();
    await this.app.attempt(async () => {
      for (const name of add) await this.app.store.api.post(`/projects/${p}/members`, { name });
      for (const name of remove) await this.app.store.api.delete(`/projects/${p}/members/${encodeURIComponent(name)}`);
    }, `Saved: ${add.length} added, ${remove.length} removed.`);
    await this.saved();
  }
}

/**
 * ProfileSheet is the one place in the chat app with settings, and they are
 * the person's own: their sign-ins, signing in another phone, signing out
 * (docs/design.md, "The chat app", rule 8).
 */
export class ProfileSheet extends Component {
  private signins: Signin[] | null = null;
  private phone: { url: string; qr?: string; expires_at: string } | null = null;
  private error = '';

  constructor(private readonly app: ChatApp) {
    super('div');
    this.actions = {
      close: () => this.app.layer.close(),
      'sign-out-one': async (t) => {
        await this.app.attempt(() => this.app.store.api.delete(`/me/signins/${t.dataset['v']}`), 'Signed out.');
        await this.loadSignins();
      },
      'sign-out': async () => {
        await this.app.attempt(() => this.app.store.signOut());
        this.app.showSignIn('You are signed out.');
      },
      'phone-code': () => this.showPhoneCode(),
    };
  }

  protected view(): Html {
    const me = this.app.store.me;
    const signins = this.signins === null ? html`<p class="${look.note}">Loading…</p>`
      : this.signins.map((s) => html`<div class="${look.mem}"><div class="min-w-0 flex-1"><div class="${look.memName}">${s.what}${s.current ? ' · this one' : ''}</div><div class="${look.memSub}">${s.surface === 'admin' ? 'admin UI' : 'chat app'} · ${s.from} · ${time.ago(s.last_used)}</div></div>${s.current ? '' : html`<button class="${ui.btn('tonal', 'sm')}" data-act="sign-out-one" data-v="${String(s.id)}">Sign out</button>`}</div>`);
    // The QR code stays black on white in both themes, for the camera.
    const phone = this.phone
      ? html`<div class="flex flex-wrap items-center gap-3.5 px-4 pt-2 pb-3"><div class="w-40 max-w-full shrink-0 rounded-xl bg-white p-2">${this.phone.qr ? html`<img class="block w-full [image-rendering:pixelated]" src="${this.phone.qr}" alt="QR code that signs in a phone">` : ''}</div><p class="flex-[1_1_180px] text-[13px] text-on-surface-variant">Scan it with the phone's camera. It works once, ${time.until(this.phone.expires_at)}, and signs that phone in as you.</p></div>`
      : html`<div class="${look.pad}"><button class="${ui.btn('filled')}" data-act="phone-code">Show a code</button>
          ${this.error ? html`<p class="${ui.error} mt-2">${this.error}</p>` : ''}</div>`;
    return html`<div class="${look.sheetHead}">${me ? Avatar.of(me) : ''}<div class="min-w-0 flex-1"><h3>${me?.display_name || me?.name || ''}</h3><p>@${me?.name ?? ''}${me?.admin ? ' · admin' : ''}</p></div>${closeButton()}</div>
      <div class="${look.sheetScroll}">
        <div class="${look.sec}">Sign in another phone</div>${phone}
        <div class="${look.sec}">Signed in</div>${signins}
        <div class="${look.pad}"><button class="${ui.btn('tonal')}" data-act="sign-out">Sign out of this browser</button></div>
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
    return html`<div class="${ui.card} w-[min(380px,100%)] gap-4 p-6">
      <div class="flex items-center gap-2 text-xl font-bold tracking-[-.01em] [&_svg]:size-[26px] [&_svg]:text-primary">${Icon.logo} cafard</div>
      ${this.message ? html`<p class="${ui.error}" role="alert">${this.message}</p>` : ''}
      <p class="text-sm">Open the sign-in link an admin sent you, or scan the code from a phone that is signed in: <span class="text-on-surface-variant">profile, Sign in another phone</span>.</p></div>`;
  }

  focus(): void {}
}
