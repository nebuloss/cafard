import { Component, mayAutofocus } from '@shared/component';
import { Serial } from '@shared/live';
import { time } from '@shared/format';
import { Html, escape, html } from '@shared/html';
import { n, t } from '@shared/i18n';
import { Icon } from '@shared/icons';
import { PickerState, PickerView } from '@shared/picker';
import { Toaster } from '@shared/toast';
import { ui } from '@shared/ui';
import { Avatar } from '@shared/widgets';
import type { AdminApp } from './app';
import { closeButton, memberItems, projectItems } from './pickers';
import type { Kind, Link, Member, MemberRef } from './types';

/** What a kind of member is, under its name. */
function describe(k: Kind): string {
  switch (k) {
    case 'person':
      return t('Signs in to the chat app.');
    case 'agent':
      return t('A machine whose MCP clients read and write.');
    case 'device':
      return t('A script or board that only sends.');
  }
}

/** A kind of member's name, on its card. */
function kindName(k: Kind): string {
  switch (k) {
    case 'person':
      return t('Person');
    case 'agent':
      return t('Agent');
    case 'device':
      return t('Device');
  }
}

/** The title of the dialog that makes one. */
function newTitle(k: Kind): string {
  switch (k) {
    case 'person':
      return t('New person');
    case 'agent':
      return t('New agent');
    case 'device':
      return t('New device');
  }
}

/** A dialog's head: its title, a line under it, the close button. */
function head(title: string, sub: string): Html {
  return html`<div class="${ui.dialogHead}"><div class="min-w-0 flex-1"><h2>${title}</h2><p>${sub}</p></div>${closeButton()}</div>`;
}

const FIELD_HELP = 'text-[12.5px] text-on-surface-variant';

/**
 * The MCP clients an agent's command can set up, one per command, as the
 * join link knows them (internal/httpapi/join/clients.go, Clients): the
 * server refuses an id it does not know, so a client added there is added
 * here too.
 */
const CLIENTS: { id: string; name: string }[] = [
  { id: 'claude-code', name: 'Claude Code' },
  { id: 'codex', name: 'Codex' },
  { id: 'crush', name: 'crush' },
  { id: 'opencode', name: 'opencode' },
];

/** The command that runs an agent's link for one client, as internal/install's Command writes it. */
function installCommand(link: string, windows: boolean, client: string): string {
  // A device's link sets up no MCP client: no client in its query.
  const q = client ? `&client=${client}` : '';
  return windows ? `irm '${link}?shell=ps1${q}' | iex` : `curl -fsSL '${link}?shell=sh${q}' | sh`;
}

/** Text to copy, with its button. */
function copyBox(text: string): Html {
  return html`<div class="flex items-stretch overflow-hidden rounded-md3-s border border-outline-variant bg-surface-container"><code class="min-w-0 flex-1 overflow-x-auto px-3 py-2.5 text-[12.5px] whitespace-nowrap">${text}</code><button class="state-layer shrink-0 border-l border-outline-variant px-3.5 text-[13px] font-semibold text-primary" data-act="copy" data-v="${text}">${t('Copy')}</button></div>`;
}

/** Copies text, or says how to. */
async function copy(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    Toaster.show(t('Copied.'));
  } catch {
    Toaster.show(t('Select the text to copy it.'));
  }
}

/**
 * CreateMemberFlow is New → person, agent or device → details → what to
 * give them. It ends on a GiveScreen, which every way of making a link or a
 * token ends on.
 */
export class CreateMemberFlow extends Component {
  private kind: Kind | null = null;
  private picker: PickerView | null = null;
  private error = '';

  constructor(private readonly app: AdminApp) {
    super('div');
    this.actions = {
      close: () => this.app.layer.close(),
      'pick-kind': (el) => {
        this.kind = (el.dataset['v'] ?? 'person') as Kind;
        this.picker = new PickerView(new PickerState(projectItems(this.app.store.projects, new Set()), []), { kinds: false, inline: true, placeholder: t('Search projects') });
        this.error = '';
        this.render();
      },
      back: () => {
        this.kind = null;
        this.render();
      },
      create: () => this.create(),
    };
    this.root.addEventListener('submit', (e) => {
      e.preventDefault();
      void this.create();
    });
  }

  protected view(): Html {
    if (!this.kind) {
      const cards = (['person', 'agent', 'device'] as Kind[]).map((k) =>
        html`<button class="state-layer flex flex-col gap-2 rounded-md3-l border border-outline-variant p-3.5 text-left" data-act="pick-kind" data-v="${k}">${Avatar.of({ kind: k, name: k === 'person' ? 'P' : k })}<b class="text-[15px]">${kindName(k)}</b><span class="${FIELD_HELP}">${describe(k)}</span></button>`);
      return html`${head(t('New member'), t('One agent per machine, not per project.'))}
        <div class="${ui.dialogBody}"><div class="grid gap-2.5 [grid-template-columns:repeat(auto-fit,minmax(150px,1fr))]">${cards}</div></div>`;
    }
    const k = this.kind;
    const fields = k === 'person'
      ? html`<div class="grid gap-3 sm:grid-cols-2"><div class="${ui.field}"><label class="${ui.label}" for="nDisplay">${t('Name')}</label><input class="${ui.input}" id="nDisplay" placeholder="Camille Durand" autocomplete="off"></div>
         <div class="${ui.field}"><label class="${ui.label}" for="nName">${t('Username')}</label><input class="${ui.input} font-mono" id="nName" placeholder="camille" autocomplete="off"></div></div>`
      : html`<div class="${ui.field}"><label class="${ui.label}" for="nName">${t('Name')}</label><input class="${ui.input} font-mono" id="nName" placeholder="${k === 'agent' ? 'lab-bench' : 'be98-dut'}" autocomplete="off"></div>
         <div class="${ui.field}"><label class="${ui.label}" for="nDesc">${k === 'agent' ? t('What it can do') : t('Note')}</label><textarea class="${ui.textarea}" id="nDesc" placeholder="${k === 'agent' ? t('Serial console of the be98 test unit; can flash and power-cycle it.') : t('The be98 test unit, through curl.')}"></textarea></div>`;
    return html`${head(newTitle(k), describe(k))}
      <form class="${ui.dialogBody}" novalidate>${fields}
        <span class="${ui.error}" id="nErr" ${Html.raw(this.error ? '' : 'hidden')}>${this.error}</span>
        <div class="${ui.field}"><span class="${ui.label}">${t('Projects')}</span><div class="pk-slot"></div></div>
        ${k !== 'device' ? html`<div class="${ui.field}"><label class="${ui.label}" for="nExp">${t('The link works for')}</label><select class="${ui.input}" id="nExp"><option value="1">${n(1, '{n} hour', '{n} hours')}</option><option value="24" selected>${n(24, '{n} hour', '{n} hours')}</option><option value="168">${n(7, '{n} day', '{n} days')}</option></select></div>` : ''}
      </form>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('tonal')}" data-act="back">${t('Back')}</button><button class="${ui.btn('filled')}" data-act="create">${t('Create')}</button></div>`;
  }

  protected override afterRender(): void {
    const slot = this.find('.pk-slot');
    if (slot && this.picker) {
      slot.replaceWith(this.picker.root);
      this.picker.render();
    }
    if (mayAutofocus()) this.find<HTMLInputElement>('#nDisplay, #nName')?.focus();
  }

  private value(id: string): string | undefined {
    return this.find<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(id)?.value.trim();
  }

  private async create(): Promise<void> {
    if (!this.kind || !this.picker) return;
    const body: Record<string, unknown> = {
      kind: this.kind,
      name: (this.value('#nName') ?? '').toLowerCase(),
      projects: [...this.picker.state.selected],
    };
    if (this.value('#nDisplay') !== undefined) body['display_name'] = this.value('#nDisplay');
    if (this.value('#nDesc') !== undefined) body['description'] = this.value('#nDesc');
    if (this.value('#nExp') !== undefined) body['link_hours'] = Number(this.value('#nExp'));
    try {
      const r = await this.app.store.api.post<{ member: MemberRef; link?: Link; token?: string }>('/members', body);
      await this.app.go(`/members/${encodeURIComponent(r.member.name)}`);
      const m = this.app.store.member(r.member.name);
      this.app.layer.open(new GiveScreen(this.app, m ?? ({ ...r.member, enabled: true, joined: false, projects: [] } as Member), r.link, r.token, body['projects'] as string[]), 'center');
    } catch (e) {
      const err = this.$('#nErr');
      err.hidden = false;
      err.textContent = e instanceof Error ? e.message : String(e);
      this.find('#nName')?.setAttribute('aria-invalid', 'true');
    }
  }
}

/**
 * GiveScreen is what to hand over: a person's or an agent's one-time link
 * with its QR code, watched until it is used; or a device's token, shown
 * this once.
 */
export class GiveScreen extends Component {
  private timer: ReturnType<typeof setInterval> | undefined;
  private unlisten: (() => void) | undefined;
  private state: Link['state'] = 'waiting';
  private usedAt = '';
  private usedFrom = '';
  /** The client an agent's commands set up. */
  private client = CLIENTS[0]!.id;

  constructor(
    private readonly app: AdminApp,
    private readonly member: Member,
    private readonly link?: Link,
    private readonly token?: string,
    private readonly projects: string[] = member.projects,
  ) {
    super('div');
    this.actions = {
      close: () => this.app.layer.close(),
      done: () => this.app.layer.close(),
      copy: (el) => copy(el.dataset['v'] ?? ''),
      client: (el) => {
        this.client = el.dataset['v'] ?? this.client;
        this.render();
      },
    };
    if (link) this.watch(link.id);
  }

  protected view(): Html {
    const m = this.member;
    const who = m.display_name || m.name;
    if (this.token) {
      const url = this.app.store.config?.public_url ?? location.origin;
      const project = this.projects[0] ?? '<project>';
      return html`${head(t('Token for {name}', { name: who }), t('Shown this once; cafard keeps only its hash.'))}
        <div class="${ui.dialogBody}">
          <div class="${ui.field}"><span class="${ui.label}">${t('Token')}</span>${copyBox(this.token)}</div>
          <div class="${ui.field}"><span class="${ui.label}">${t('Send a file')}</span><pre class="m-0 overflow-x-auto rounded-md3-s bg-surface-container p-3 font-mono text-[12.5px]">curl -T boot.log -H "Authorization: Bearer ${this.token}" \\\n  ${url}/api/v1/send/${project}/&lt;conversation&gt;/</pre></div></div>
        <div class="${ui.dialogFoot}"><button class="${ui.btn('filled')}" data-act="done">${t('I saved the token')}</button></div>`;
    }
    const l = this.link;
    if (!l) return Html.empty;
    const chip = (c: { id: string; name: string }) => {
      const on = this.client === c.id;
      return html`<button type="button" class="state-layer inline-flex h-8 items-center gap-1.5 rounded-md3-s px-3 text-[13px] font-medium ${on ? 'bg-secondary-container text-on-secondary-container' : 'text-on-surface-variant ring-1 ring-outline-variant ring-inset'}" role="radio" aria-checked="${String(on)}" data-act="client" data-v="${c.id}">${on ? Icon.of('check') : ''}${c.name}</button>`;
    };
    const right = m.kind === 'device'
      ? html`<div class="${ui.field}"><span class="${ui.label}">Linux / macOS</span>${copyBox(installCommand(l.url ?? '', false, ''))}</div>
        <div class="${ui.field}"><span class="${ui.label}">Windows (PowerShell)</span>${copyBox(installCommand(l.url ?? '', true, ''))}</div>
        <p class="${FIELD_HELP}">${t('Run on {name}: it installs mouchard with its token, so it sends with mouchard say, send, log and run.', { name: m.name })}</p>`
      : m.kind === 'agent'
      ? html`<div class="${ui.field}"><span class="${ui.label}">${t('Client')}</span><div class="flex flex-wrap gap-2 [&_.ic]:size-4" role="radiogroup">${CLIENTS.map(chip)}</div></div>
        <div class="${ui.field}"><span class="${ui.label}">Linux / macOS</span>${copyBox(installCommand(l.url ?? '', false, this.client))}</div>
        <div class="${ui.field}"><span class="${ui.label}">Windows (PowerShell)</span>${copyBox(installCommand(l.url ?? '', true, this.client))}</div>
        <p class="${FIELD_HELP}">${t('Run on {name}, as the user its agents run as.', { name: m.name })}</p>`
      : html`<div class="${ui.field}"><span class="${ui.label}">${t('Sign-in link')}</span>${copyBox(l.url ?? '')}</div><p class="text-[12.5px] text-on-surface-variant">${t('{name} opens it, or scans the code, on the device they will use, and is signed in. No password.', { name: who })}</p>`;
    const STATUS = 'flex items-center gap-2 rounded-md3-s px-3 py-2.5 text-[13.5px]';
    const dot = (on: boolean) => html`<span class="size-2 rounded-full ${on ? 'bg-success' : 'bg-warning animate-[pulse_1.4s_infinite]'}"></span>`;
    const status = this.state === 'used'
      ? html`<div class="${STATUS} bg-success-container text-on-success-container">${dot(true)}${t('Used by {name} {ago} from {from}.', { name: m.name, ago: time.ago(this.usedAt), from: this.usedFrom })}</div>`
      : this.state === 'waiting'
        ? html`<div class="${STATUS} bg-surface-container">${dot(false)}${t('Waiting · {until}', { until: time.until(l.expires_at) })}</div>`
        : html`<div class="${STATUS} bg-surface-container">${Icon.of('warn')}${this.state === 'expired' ? t('The link expired.') : t('The link was cancelled.')}</div>`;
    return html`${head(m.kind !== 'person' ? t('Connect {name}', { name: who }) : t('Send this to {name}', { name: who }), t('One-time link. Never paste it in a conversation.'))}
      <div class="${ui.dialogBody}"><div class="grid items-start gap-5 sm:grid-cols-[180px_minmax(0,1fr)]"><div class="relative grid aspect-square w-[180px] max-w-full place-items-center justify-self-center rounded-md3-m border border-outline-variant bg-white p-2.5 [&_img]:w-full">${l.qr ? html`<img src="${l.qr}" alt="${t('QR code of the link')}">` : ''}${this.state === 'used' ? html`<span class="absolute inset-0 grid place-items-center rounded-md3-m bg-white/85 text-lg font-bold text-[#1c1b19]">${t('Used')}</span>` : ''}</div><div class="flex min-w-0 flex-col gap-3">${right}</div></div>${status}</div>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('filled')}" data-act="done">${t('Done')}</button></div>`;
  }

  /**
   * Watches the link on the server until it is used or ends: the admin's
   * event stream says when something changed, and a slow timer catches the
   * link expiring, which nothing writes.
   */
  private watch(id: number): void {
    const check = new Serial(() => this.check(id));
    this.unlisten = this.app.onLive(() => void check.run());
    this.timer = setInterval(() => void check.run(), 30_000);
  }

  private async check(id: number): Promise<void> {
    {
      if (!this.root.isConnected || this.timer === undefined) return this.stop();
      let members: Member[];
      try {
        members = (await this.app.store.api.get<{ members: Member[] }>('/members')).members;
      } catch {
        return;
      }
      const l = members.find((x) => x.name === this.member.name)?.link;
      if (!l || l.id !== id || l.state === 'waiting') return;
      this.stop();
      // Used: there is nothing left to hand over, so the dialog goes, and
      // says so. Expired or cancelled, it stays to say that.
      if (l.state === 'used') {
        this.app.layer.close();
        await this.app.refresh();
        const name = this.member.kind !== 'person' ? this.member.name : this.member.display_name || this.member.name;
        const from = l.used_from ?? '';
        Toaster.show(this.member.kind !== 'person'
          ? from ? t('{name} is connected from {from}.', { name, from }) : t('{name} is connected.', { name })
          : from ? t('{name} is signed in from {from}.', { name, from }) : t('{name} is signed in.', { name }));
        return;
      }
      this.state = l.state;
      this.usedAt = l.used_at ?? '';
      this.usedFrom = l.used_from ?? '';
      this.render();
      await this.app.refresh();
    }
  }

  private stop(): void {
    clearInterval(this.timer);
    this.timer = undefined;
    this.unlisten?.();
    this.unlisten = undefined;
  }
}

/** CreateProjectFlow is New project: a name, members, retention and quota. */
export class CreateProjectFlow extends Component {
  private readonly picker: PickerView;
  private error = '';

  constructor(private readonly app: AdminApp) {
    super('div');
    this.picker = new PickerView(new PickerState(memberItems(app.store.members), []), { kinds: true, inline: true, placeholder: t('Search by name or what it does') });
    this.actions = {
      close: () => this.app.layer.close(),
      create: () => this.create(),
    };
    this.root.addEventListener('submit', (e) => {
      e.preventDefault();
      void this.create();
    });
    this.root.addEventListener('input', (e) => {
      const el = e.target as HTMLInputElement;
      if (el.id === 'npName') this.root.querySelectorAll('.np-hint').forEach((h) => (h.textContent = el.value.trim() || 'speedtest'));
    });
  }

  protected view(): Html {
    const ret = [7, 14, 30, 60, 90].map((d) => html`<option value="${String(d)}" ${Html.raw(d === 30 ? 'selected' : '')}>${n(d, '{n} day', '{n} days')}</option>`);
    const quota = [1, 2, 5, 10, 20].map((g) => html`<option value="${String(g * 2 ** 30)}" ${Html.raw(g === 5 ? 'selected' : '')}>${t('{n} GB', { n: g })}</option>`);
    // The project's name is a span the name field fills as it is typed, so
    // the sentence is translated whole and the span put in its {project}.
    const hint = Html.raw(escape(t('Agents use it with “check cafard, project {project}”.')).replace('{project}', '<span class="np-hint font-mono">speedtest</span>'));
    return html`${head(t('New project'), t('Its people and agents see every conversation in it; devices only send.'))}
      <form class="${ui.dialogBody}" novalidate>
        <div class="${ui.field}"><label class="${ui.label}" for="npName">${t('Name')}</label><input class="${ui.input} font-mono" id="npName" placeholder="speedtest" autocomplete="off"><span class="${ui.error}" id="npErr" ${Html.raw(this.error ? '' : 'hidden')}>${this.error}</span></div>
        <div class="${ui.field}"><span class="${ui.label}">${t('Members')}</span><div class="pk-slot"></div></div>
        <div class="grid gap-3 sm:grid-cols-2"><div class="${ui.field}"><label class="${ui.label}" for="npRet">${t('Retention')}</label><select class="${ui.input}" id="npRet">${ret}</select></div>
          <div class="${ui.field}"><label class="${ui.label}" for="npQuota">${t('Quota')}</label><select class="${ui.input}" id="npQuota">${quota}</select></div></div>
        <p class="${FIELD_HELP}">${hint}</p>
      </form>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('tonal')}" data-act="close">${t('Cancel')}</button><button class="${ui.btn('filled')}" data-act="create">${t('Create project')}</button></div>`;
  }

  protected override afterRender(): void {
    this.$('.pk-slot').replaceWith(this.picker.root);
    this.picker.render();
    if (mayAutofocus()) this.find<HTMLInputElement>('#npName')?.focus();
  }

  private async create(): Promise<void> {
    const name = this.$<HTMLInputElement>('#npName').value.trim().toLowerCase();
    try {
      await this.app.store.api.post('/projects', {
        name,
        members: [...this.picker.state.selected],
        retention_days: Number(this.$<HTMLSelectElement>('#npRet').value),
        quota_bytes: Number(this.$<HTMLSelectElement>('#npQuota').value),
      });
    } catch (e) {
      const err = this.$('#npErr');
      err.hidden = false;
      err.textContent = e instanceof Error ? e.message : String(e);
      this.find('#npName')?.setAttribute('aria-invalid', 'true');
      return;
    }
    await this.app.go(`/projects/${encodeURIComponent(name)}`);
    Toaster.show(t('{name} is ready.', { name }));
  }
}
