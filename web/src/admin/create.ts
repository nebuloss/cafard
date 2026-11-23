import { Component } from '@shared/component';
import { time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { PickerState, PickerView } from '@shared/picker';
import { Toaster } from '@shared/toast';
import { ui } from '@shared/ui';
import { Avatar } from '@shared/widgets';
import type { AdminApp } from './app';
import { closeButton, memberItems, projectItems } from './pickers';
import type { Kind, Link, Member, MemberRef } from './types';

const DESCRIBE: Record<Kind, string> = {
  person: 'Signs in to the chat app.',
  agent: 'A machine whose MCP clients read and write.',
  device: 'A script or board that only sends.',
};

/** A dialog's head: its title, a line under it, the close button. */
function head(title: string, sub: string): Html {
  return html`<div class="${ui.dialogHead}"><div class="min-w-0 flex-1"><h2>${title}</h2><p>${sub}</p></div>${closeButton()}</div>`;
}

const FIELD_HELP = 'text-[12.5px] text-on-surface-variant';

/** Text to copy, with its button. */
function copyBox(text: string): Html {
  return html`<div class="flex items-stretch overflow-hidden rounded-md3-s border border-outline-variant bg-surface-container"><code class="min-w-0 flex-1 overflow-x-auto px-3 py-2.5 text-[12.5px] whitespace-nowrap">${text}</code><button class="state-layer shrink-0 border-l border-outline-variant px-3.5 text-[13px] font-semibold text-primary" data-act="copy" data-v="${text}">Copy</button></div>`;
}

/** Copies text, or says how to. */
async function copy(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    Toaster.show('Copied.');
  } catch {
    Toaster.show('Select the text to copy it.');
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
      'pick-kind': (t) => {
        this.kind = (t.dataset['v'] ?? 'person') as Kind;
        this.picker = new PickerView(new PickerState(projectItems(this.app.store.projects, new Set()), []), { kinds: false, inline: true, placeholder: 'Search projects' });
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
        html`<button class="state-layer flex flex-col gap-2 rounded-md3-l border border-outline-variant p-3.5 text-left" data-act="pick-kind" data-v="${k}">${Avatar.of({ kind: k, name: k === 'person' ? 'P' : k })}<b class="text-[15px]">${k[0]?.toUpperCase()}${k.slice(1)}</b><span class="${FIELD_HELP}">${DESCRIBE[k]}</span></button>`);
      return html`${head('New member', 'One agent per machine, not per project.')}
        <div class="${ui.dialogBody}"><div class="grid gap-2.5 [grid-template-columns:repeat(auto-fit,minmax(150px,1fr))]">${cards}</div></div>`;
    }
    const k = this.kind;
    const fields = k === 'person'
      ? html`<div class="grid gap-3 sm:grid-cols-2"><div class="${ui.field}"><label class="${ui.label}" for="nDisplay">Name</label><input class="${ui.input}" id="nDisplay" placeholder="Camille Durand" autocomplete="off"></div>
         <div class="${ui.field}"><label class="${ui.label}" for="nName">Username</label><input class="${ui.input} font-mono" id="nName" placeholder="camille" autocomplete="off"></div></div>`
      : html`<div class="${ui.field}"><label class="${ui.label}" for="nName">Name</label><input class="${ui.input} font-mono" id="nName" placeholder="${k === 'agent' ? 'lab-bench' : 'be98-dut'}" autocomplete="off"></div>
         <div class="${ui.field}"><label class="${ui.label}" for="nDesc">${k === 'agent' ? 'What it can do' : 'Note'}</label><textarea class="${ui.textarea}" id="nDesc" placeholder="${k === 'agent' ? 'Serial console of the be98 test unit; can flash and power-cycle it.' : 'The be98 test unit, through curl.'}"></textarea></div>`;
    return html`${head(`New ${k}`, DESCRIBE[k])}
      <form class="${ui.dialogBody}" novalidate>${fields}
        <span class="${ui.error}" id="nErr" ${Html.raw(this.error ? '' : 'hidden')}>${this.error}</span>
        <div class="${ui.field}"><span class="${ui.label}">Projects</span><div class="pk-slot"></div></div>
        ${k !== 'device' ? html`<div class="${ui.field}"><label class="${ui.label}" for="nExp">The link works for</label><select class="${ui.input}" id="nExp"><option value="1">1 hour</option><option value="24" selected>24 hours</option><option value="168">7 days</option></select></div>` : ''}
      </form>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('tonal')}" data-act="back">Back</button><button class="${ui.btn('filled')}" data-act="create">Create</button></div>`;
  }

  protected override afterRender(): void {
    const slot = this.find('.pk-slot');
    if (slot && this.picker) {
      slot.replaceWith(this.picker.root);
      this.picker.render();
    }
    this.find<HTMLInputElement>('#nDisplay, #nName')?.focus();
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
  private state: Link['state'] = 'waiting';
  private usedAt = '';
  private usedFrom = '';

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
      copy: (t) => copy(t.dataset['v'] ?? ''),
    };
    if (link) this.watch(link.id);
  }

  protected view(): Html {
    const m = this.member;
    const who = m.display_name || m.name;
    if (this.token) {
      const url = this.app.store.config?.public_url ?? location.origin;
      const project = this.projects[0] ?? '<project>';
      return html`${head(`Token for ${who}`, 'Shown this once; cafard keeps only its hash.')}
        <div class="${ui.dialogBody}">
          <div class="${ui.field}"><span class="${ui.label}">Token</span>${copyBox(this.token)}</div>
          <div class="${ui.field}"><span class="${ui.label}">Send a file</span><pre class="m-0 overflow-x-auto rounded-md3-s bg-surface-container p-3 font-mono text-[12.5px]">curl -T boot.log -H "Authorization: Bearer ${this.token}" \\\n  ${url}/api/v1/send/${project}/&lt;conversation&gt;/</pre></div></div>
        <div class="${ui.dialogFoot}"><button class="${ui.btn('filled')}" data-act="done">I saved the token</button></div>`;
    }
    const l = this.link;
    if (!l) return Html.empty;
    const right = m.kind === 'agent'
      ? html`<div class="${ui.field}"><span class="${ui.label}">Run on ${m.name}, as the user its agents run as</span>${copyBox(l.command ?? '')}</div>`
      : html`<div class="${ui.field}"><span class="${ui.label}">Sign-in link</span>${copyBox(l.url ?? '')}</div><p class="text-[12.5px] text-on-surface-variant">${who} opens it, or scans the code, on the device they will use, and is signed in. No password.</p>`;
    const STATUS = 'flex items-center gap-2 rounded-md3-s px-3 py-2.5 text-[13.5px]';
    const dot = (on: boolean) => html`<span class="size-2 rounded-full ${on ? 'bg-success' : 'bg-warning animate-[pulse_1.4s_infinite]'}"></span>`;
    const status = this.state === 'used'
      ? html`<div class="${STATUS} bg-success-container text-on-success-container">${dot(true)}Used by ${m.name} ${time.ago(this.usedAt)} from ${this.usedFrom}.</div>`
      : this.state === 'waiting'
        ? html`<div class="${STATUS} bg-surface-container">${dot(false)}Waiting · ${time.until(l.expires_at)}</div>`
        : html`<div class="${STATUS} bg-surface-container">${Icon.of('warn')}The link is ${this.state}.</div>`;
    return html`${head(`${m.kind === 'agent' ? 'Connect' : 'Send this to'} ${who}`, 'One-time link. Never paste it in a conversation.')}
      <div class="${ui.dialogBody}"><div class="grid items-start gap-5 sm:grid-cols-[180px_minmax(0,1fr)]"><div class="relative grid aspect-square w-[180px] max-w-full place-items-center justify-self-center rounded-md3-m border border-outline-variant bg-white p-2.5 [&_img]:w-full">${l.qr ? html`<img src="${l.qr}" alt="QR code of the link">` : ''}${this.state === 'used' ? html`<span class="absolute inset-0 grid place-items-center rounded-md3-m bg-white/85 text-lg font-bold text-[#1c1b19]">Used</span>` : ''}</div><div class="flex min-w-0 flex-col gap-3">${right}</div></div>${status}</div>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('filled')}" data-act="done">Done</button></div>`;
  }

  /** Watches the link on the server until it is used or ends. */
  private watch(id: number): void {
    this.timer = setInterval(async () => {
      if (!this.root.isConnected) return this.stop();
      let members: Member[];
      try {
        members = (await this.app.store.api.get<{ members: Member[] }>('/members')).members;
      } catch {
        return;
      }
      const l = members.find((x) => x.name === this.member.name)?.link;
      if (!l || l.id !== id || l.state === 'waiting') return;
      this.stop();
      this.state = l.state;
      this.usedAt = l.used_at ?? '';
      this.usedFrom = l.used_from ?? '';
      this.render();
      await this.app.refresh();
    }, 3000);
  }

  private stop(): void {
    clearInterval(this.timer);
    this.timer = undefined;
  }
}

/** CreateProjectFlow is New project: a name, members, retention and quota. */
export class CreateProjectFlow extends Component {
  private readonly picker: PickerView;
  private error = '';

  constructor(private readonly app: AdminApp) {
    super('div');
    this.picker = new PickerView(new PickerState(memberItems(app.store.members), []), { kinds: true, inline: true, placeholder: 'Search by name or what it does' });
    this.actions = {
      close: () => this.app.layer.close(),
      create: () => this.create(),
    };
    this.root.addEventListener('submit', (e) => {
      e.preventDefault();
      void this.create();
    });
    this.root.addEventListener('input', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.id === 'npName') this.root.querySelectorAll('.np-hint').forEach((h) => (h.textContent = t.value.trim() || 'speedtest'));
    });
  }

  protected view(): Html {
    const ret = [7, 14, 30, 60, 90].map((d) => html`<option value="${String(d)}" ${Html.raw(d === 30 ? 'selected' : '')}>${d} days</option>`);
    const quota = [1, 2, 5, 10, 20].map((g) => html`<option value="${String(g * 2 ** 30)}" ${Html.raw(g === 5 ? 'selected' : '')}>${g} GB</option>`);
    return html`${head('New project', 'Its people and agents see every conversation in it; devices only send.')}
      <form class="${ui.dialogBody}" novalidate>
        <div class="${ui.field}"><label class="${ui.label}" for="npName">Name</label><input class="${ui.input} font-mono" id="npName" placeholder="speedtest" autocomplete="off"><span class="${ui.error}" id="npErr" ${Html.raw(this.error ? '' : 'hidden')}>${this.error}</span></div>
        <div class="${ui.field}"><span class="${ui.label}">Members</span><div class="pk-slot"></div></div>
        <div class="grid gap-3 sm:grid-cols-2"><div class="${ui.field}"><label class="${ui.label}" for="npRet">Retention</label><select class="${ui.input}" id="npRet">${ret}</select></div>
          <div class="${ui.field}"><label class="${ui.label}" for="npQuota">Quota</label><select class="${ui.input}" id="npQuota">${quota}</select></div></div>
        <p class="${FIELD_HELP}">Agents use it with “check cafard, project <span class="np-hint font-mono">speedtest</span>”.</p>
      </form>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('tonal')}" data-act="close">Cancel</button><button class="${ui.btn('filled')}" data-act="create">Create project</button></div>`;
  }

  protected override afterRender(): void {
    this.$('.pk-slot').replaceWith(this.picker.root);
    this.picker.render();
    this.find<HTMLInputElement>('#npName')?.focus();
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
    Toaster.show(`${name} is ready.`);
  }
}
