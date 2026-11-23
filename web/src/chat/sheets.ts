import { Refused } from '@shared/api';
import { Component } from '@shared/component';
import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { PickerState, PickerView } from '@shared/picker';
import { Toaster } from '@shared/toast';
import { Avatar } from '@shared/widgets';
import type { ChatApp } from './app';
import type { Address } from './store';
import type { FileInfo, LinesPage, Member, Message, Signin } from './types';

function closeButton(): Html {
  return html`<button class="icon-btn" data-act="close" aria-label="Close">${Icon.of('close')}</button>`;
}

/** Errors as people read them. */
function reason(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// The log viewer colours lines that look like trouble, the way the mockup
// does; it decides nothing, it only helps the eye.
const ERR = /\b(error|errors|fail|failed|failure|panic|fatal|timeout|timed out|denied|exception|segfault|oops|hang)\b/i;
const WARN = /\bwarn(ing)?\b/i;

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
    return html`<div class="sheet-head"><div class="t"><h3>${f.name}</h3><p class="log-meta">${this.meta()}</p></div>${closeButton()}</div>
      <div class="log-tools"><label class="search">${Icon.of('search')}<input class="log-q" type="search" placeholder="Search in this log" value="${this.query}" autocomplete="off"></label>
        <span class="log-count"></span>
        <button class="btn tonal sm" data-act="start">Start</button><button class="btn tonal sm" data-act="end">End</button>
        ${f.state === 'live' ? html`<label class="switch"><input type="checkbox" class="log-follow" ${Html.raw(this.follow ? 'checked' : '')}> Follow live</label>` : ''}</div>
      <div class="logbody"><p class="loading">Loading…</p></div>`;
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
      this.$('.logbody').innerHTML = html`<p class="empty">${reason(e)}</p>`.value;
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
      const cls = ERR.test(l.text) ? 'err' : WARN.test(l.text) ? 'warn' : '';
      return html`<div class="ln ${cls}"><span class="n">${String(l.n)}</span><span class="t">${q ? this.highlight(l.text, q) : l.text || ' '}${l.cut ? ' …' : ''}</span></div>`;
    });
    const more = this.page.next ? html`<button class="btn tonal sm more" data-act="more">More</button>` : Html.empty;
    body.innerHTML = html`${rows.length ? rows : html`<p class="empty">${this.mode === 'search' ? 'No line matches.' : 'Empty.'}</p>`}${more}`.value;
    this.$('.log-count').textContent = this.mode === 'search' ? `${this.page.matches ?? 0} matching line${this.page.matches === 1 ? '' : 's'}` : '';
    this.$('.log-meta').textContent = this.meta();
    if (toEnd) body.scrollTop = body.scrollHeight;
  }

  private highlight(text: string, q: string): Html {
    const i = text.toLowerCase().indexOf(q);
    if (i < 0) return html`${text}`;
    return html`${text.slice(0, i)}<mark>${text.slice(i, i + q.length)}</mark>${text.slice(i + q.length)}`;
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
      ? html`<button class="lb-nav l" data-act="prev" aria-label="Previous">${Icon.of('back')}</button><button class="lb-nav r" data-act="next" aria-label="Next">${Icon.of('chev')}</button>`
      : Html.empty;
    return html`<div class="lb-head"><div class="t"><b>${f.name}</b>${by}${this.images.length > 1 ? ` · ${this.index + 1} of ${this.images.length}` : ''}</div>
      <a class="icon-btn" href="${f.url}" download="${f.name}" aria-label="Download">${Icon.of('folder')}</a>${closeButton()}</div>
      <div class="lb-img"><img src="${f.url}" alt="${f.name}">${nav}</div>`;
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
    const members = this.members.map((m) => html`<div class="mem">${Avatar.of(m)}<div class="m-main"><div class="m-name">${m.display_name || m.name}${m.kind !== 'person' ? html`<span class="kind ${m.kind}">${m.kind}</span>` : ''}</div><div class="m-sub">${m.name === me?.name ? 'You' : kindText(m)}</div></div></div>`);
    const files = this.files().filter((f) => f.state !== 'deleted' && f.state !== 'cut').map((f) => {
      const inner = html`<span class="fi">${Icon.of(f.lines_url ? 'list' : 'folder')}</span><span class="f-main"><span class="f-name">${f.name}</span><span class="f-meta">${Bytes.format(f.size)}${f.state === 'live' ? ' · live' : ''}</span></span>`;
      // A log opens in the viewer; anything else is a download.
      return f.lines_url
        ? html`<button class="file-row" data-act="log" data-v="${String(f.id)}">${inner}</button>`
        : html`<a class="file-row" href="${f.url}" download="${f.name}">${inner}</a>`;
    });
    const send = `curl -T boot.log -H "Authorization: Bearer cfd_…" ${location.origin}/api/v1/send/${this.address}/`;
    return html`<div class="sheet-head"><div class="t"><h3>${c?.title ?? this.address.name}</h3><p>${this.address.project}${c?.closed ? ' · closed' : ''}</p></div>${closeButton()}</div>
      <div class="sheet-scroll">
        <div class="sec">Address for agents and scripts</div>
        <div class="addr"><div class="row2"><code>${this.address.toString()}</code><button class="copy" data-act="copy" data-v="${this.address.toString()}">Copy</button></div>
          <p>A device sends here with <code>${send}</code></p></div>
        <div class="sec">Members · ${this.members.length}</div>${members}
        ${me?.kind === 'person' ? html`<div class="pad"><button class="btn tonal" data-act="add">Add or remove people and agents</button></div>` : ''}
        <div class="sec">Files</div>${files.length ? files : html`<p class="mem m-sub">No files yet.</p>`}
        <div class="pad"><button class="btn tonal" data-act="toggle-closed">${c?.closed ? 'Reopen conversation' : 'Close conversation'}</button></div>
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
    return html`<div class="m-head"><div class="t"><h2>Members of ${this.project}</h2><p>Tick to add, untick to remove. They see every conversation in ${this.project}.</p></div>${closeButton()}</div>
      <div class="m-body"><div class="pk-slot"></div></div>
      <div class="m-foot"><button class="btn tonal" data-act="close">Cancel</button><button class="btn filled save" data-act="save" disabled>Save</button></div>`;
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
    };
    this.root.addEventListener('submit', (e) => {
      e.preventDefault();
      void this.showPhoneCode();
    });
  }

  protected view(): Html {
    const me = this.app.store.me;
    const signins = this.signins === null ? html`<p class="mem m-sub">Loading…</p>`
      : this.signins.map((s) => html`<div class="mem"><div class="m-main"><div class="m-name">${s.what}${s.current ? ' · this one' : ''}</div><div class="m-sub">${s.surface === 'admin' ? 'admin UI' : 'chat app'} · ${s.from} · ${time.ago(s.last_used)}</div></div>${s.current ? '' : html`<button class="btn tonal sm" data-act="sign-out-one" data-v="${String(s.id)}">Sign out</button>`}</div>`);
    const phone = this.phone
      ? html`<div class="phone-code"><div class="qr">${this.phone.qr ? html`<img src="${this.phone.qr}" alt="QR code that signs in a phone">` : ''}</div><p>Scan it with the phone's camera. It works once, ${time.until(this.phone.expires_at)}, and signs that phone in as you.</p></div>`
      : html`<form class="pad phone-form" novalidate><p class="help">Shows a code a phone scans to be signed in as you, for two minutes. Your password first, so a computer left unlocked cannot do it.</p>
          <div class="row"><input class="input" type="password" name="password" autocomplete="current-password" placeholder="Your password" aria-label="Your password"><button class="btn filled" type="submit">Show code</button></div>
          ${this.error ? html`<p class="ferr">${this.error}</p>` : ''}</form>`;
    return html`<div class="sheet-head">${me ? Avatar.of(me) : ''}<div class="t"><h3>${me?.display_name || me?.name || ''}</h3><p>@${me?.name ?? ''}${me?.admin ? ' · admin' : ''}</p></div>${closeButton()}</div>
      <div class="sheet-scroll">
        <div class="sec">Sign in another phone</div>${phone}
        <div class="sec">Signed in</div>${signins}
        <div class="pad"><button class="btn tonal" data-act="sign-out">Sign out of this browser</button></div>
        <p class="help pad">To change your password, ask an admin for a password link: it signs you out everywhere too.</p>
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
    const pw = this.find<HTMLInputElement>('input[name="password"]')?.value ?? '';
    try {
      this.phone = await this.app.store.api.post<{ url: string; qr?: string; expires_at: string }>('/me/phone', { password: pw });
      this.error = '';
    } catch (e) {
      this.error = e instanceof Refused ? e.message : reason(e);
    }
    this.render();
  }
}

/** SignInView is the chat app's sign-in form. */
export class SignInView extends Component {
  constructor(
    private readonly app: ChatApp,
    private readonly message?: string,
  ) {
    super('div', 'signin');
    this.root.addEventListener('submit', (e) => {
      e.preventDefault();
      void this.submit();
    });
  }

  protected view(): Html {
    return html`<form novalidate>
      <div class="brand">${Icon.logo} cafard</div>
      <h1>Sign in</h1>
      <p>With the password you chose from your join link. No password yet? Ask an admin for a link.</p>
      ${this.message ? html`<p class="ferr" role="alert">${this.message}</p>` : ''}
      <div class="field"><label for="u">Username</label><input class="input" id="u" autocomplete="username" autocapitalize="none" required></div>
      <div class="field"><label for="p">Password</label><input class="input" id="p" type="password" autocomplete="current-password" required></div>
      <p class="ferr" id="sErr" hidden></p>
      <button class="btn filled" type="submit">Sign in</button></form>`;
  }

  focus(): void {
    this.find<HTMLInputElement>('#u')?.focus();
  }

  private async submit(): Promise<void> {
    const err = this.$('#sErr');
    try {
      await this.app.store.signIn(this.$<HTMLInputElement>('#u').value.trim(), this.$<HTMLInputElement>('#p').value);
      await this.app.showApp();
    } catch (e) {
      err.hidden = false;
      err.textContent = reason(e);
    }
  }
}
