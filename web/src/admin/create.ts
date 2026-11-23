import { Component } from '@shared/component';
import { time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { PickerState, PickerView } from '@shared/picker';
import { Toaster } from '@shared/toast';
import { Avatar } from '@shared/widgets';
import type { AdminApp } from './app';
import { closeButton, memberItems, projectItems } from './drawers';
import type { Kind, Link, Member, MemberRef } from './types';

const DESCRIBE: Record<Kind, string> = {
  person: 'Signs in to the chat app, and here if made an admin.',
  agent: 'A machine whose MCP clients (Claude Code, Codex…) read and write.',
  device: 'A script or board that only sends, with curl.',
};

function steps(n: number): Html {
  return html`<div class="steps">${[1, 2, 3].map((i) => html`<i class="${i <= n ? 'on' : ''}"></i>`)}</div>`;
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
        html`<button class="kind-card" data-act="pick-kind" data-v="${k}">${Avatar.of({ kind: k, name: k === 'person' ? 'P' : k })}<b>${k[0]?.toUpperCase()}${k.slice(1)}</b><span>${DESCRIBE[k]}</span></button>`);
      return html`<div class="m-head"><div class="t"><h2>New</h2><p>Who is it?</p>${steps(1)}</div>${closeButton()}</div>
        <div class="m-body"><div class="kinds">${cards}</div><p class="help">One agent per machine, not per project: the machine is what holds the token, and what others ask for.</p></div>`;
    }
    const k = this.kind;
    const fields = k === 'person'
      ? html`<div class="field"><label for="nDisplay">Name</label><input class="input" id="nDisplay" placeholder="Camille Durand" autocomplete="off"></div>
         <div class="field"><label for="nName">Username</label><input class="input mono" id="nName" placeholder="camille" autocomplete="off"><span class="help">What they type to sign in, and what @ finds them by. They choose their own password.</span></div>`
      : html`<div class="field"><label for="nName">Name</label><input class="input mono" id="nName" placeholder="${k === 'agent' ? 'lab-bench' : 'be98-dut'}" autocomplete="off"><span class="help">${k === 'agent' ? 'The machine’s name: people will write @name to call it.' : 'Shown as the sender of what it posts.'}</span></div>
         <div class="field"><label for="nDesc">${k === 'agent' ? 'What this machine can do' : 'Note'}</label><textarea class="input" id="nDesc" placeholder="${k === 'agent' ? 'Serial console of the be98 test unit; can flash and power-cycle it.' : 'The be98 test unit, through curl.'}"></textarea></div>`;
    return html`<div class="m-head"><div class="t"><h2>New ${k}</h2><p>${DESCRIBE[k]}</p>${steps(2)}</div>${closeButton()}</div>
      <form class="m-body" novalidate>${fields}
        <span class="ferr" id="nErr" ${Html.raw(this.error ? '' : 'hidden')}>${this.error}</span>
        <div class="field"><span class="lbl">Projects</span><span class="help">${k === 'device' ? 'It can send to these, and read nothing.' : 'It sees every conversation in these. You can change this later.'}</span><div class="pk-slot"></div></div>
        ${k !== 'device' ? html`<div class="field"><label for="nExp">The link waits</label><select class="input" id="nExp"><option value="1">1 hour</option><option value="24" selected>24 hours</option><option value="168">7 days</option></select></div>` : ''}
      </form>
      <div class="m-foot"><button class="btn tonal" data-act="back">Back</button><button class="btn filled" data-act="create">Create</button></div>`;
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
      await this.app.refresh();
      const m = this.app.store.member(r.member.name);
      this.app.layer.open(new GiveScreen(this.app, m ?? ({ ...r.member, enabled: true, joined: false, projects: [] } as Member), r.link, r.token, body['projects'] as string[]), 'center');
    } catch (e) {
      const err = this.$('#nErr');
      err.hidden = false;
      err.textContent = e instanceof Error ? e.message : String(e);
      this.find('#nName')?.classList.add('err');
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
      return html`<div class="m-head"><div class="t"><h2>Give this to ${who}</h2><p>Put the token in the script or the board’s configuration.</p>${steps(3)}</div>${closeButton()}</div>
        <div class="m-body"><div class="once">${Icon.of('warn')}<span>This is the only time the token is shown. cafard keeps only a hash of it.</span></div>
          <div class="field token-box"><span class="lbl">Token</span><div class="copybox"><code>${this.token}</code><button data-act="copy" data-v="${this.token}">Copy</button></div></div>
          <div class="field"><span class="lbl">Send a file</span><pre class="snip">curl -T boot.log -H "Authorization: Bearer ${this.token}" \\\n  ${url}/api/v1/send/${project}/&lt;conversation&gt;/</pre></div></div>
        <div class="m-foot"><button class="btn filled" data-act="done">I saved the token</button></div>`;
    }
    const l = this.link;
    if (!l) return Html.empty;
    const right = m.kind === 'agent'
      ? html`<div class="field"><span class="lbl">Run this on ${m.name}, as the user its agents run as</span><div class="copybox"><code>${l.command ?? ''}</code><button data-act="copy" data-v="${l.command ?? ''}">Copy</button></div></div>
          <ul class="does"><li>exchanges the one-time code for ${m.name}’s token</li><li>keeps it in ~/.config/cafard/token, readable by that user only</li><li>never prints the token</li></ul>`
      : html`<div class="field"><span class="lbl">${l.kind === 'password' ? 'Password link' : 'Join link'}</span><div class="copybox"><code>${l.url ?? ''}</code><button data-act="copy" data-v="${l.url ?? ''}">Copy</button></div></div>
          <p class="help">${who} scans the code with their phone, chooses a password, and can sign in.${l.kind === 'password' ? ' Every sign-in they have now ends when they use it.' : ''}</p>`;
    const status = this.state === 'used'
      ? html`<div class="status used"><span class="dot"></span>Used by ${m.name} ${time.ago(this.usedAt)} from ${this.usedFrom}. The link no longer works.</div>`
      : this.state === 'waiting'
        ? html`<div class="status"><span class="dot"></span>Waiting to be used · ${time.until(l.expires_at)}</div>`
        : html`<div class="status">${Icon.of('warn')}The link is ${this.state}.</div>`;
    return html`<div class="m-head"><div class="t"><h2>${m.kind === 'agent' ? 'Connect' : 'Send this to'} ${who}</h2><p>A one-time link. Never paste it in a conversation: everyone there, agents included, would see it.</p>${steps(3)}</div>${closeButton()}</div>
      <div class="m-body"><div class="give"><div class="qr ${this.state === 'used' ? 'used' : ''}">${l.qr ? html`<img src="${l.qr}" alt="QR code of the link">` : ''}</div><div class="give-text">${right}</div></div>${status}</div>
      <div class="m-foot"><button class="btn filled" data-act="done">Done</button></div>`;
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
    return html`<div class="m-head"><div class="t"><h2>New project</h2><p>A project decides who sees what. Its people and agents see every conversation in it; devices only send.</p></div>${closeButton()}</div>
      <form class="m-body" novalidate>
        <div class="field"><label for="npName">Name</label><input class="input mono" id="npName" placeholder="speedtest" autocomplete="off"><span class="help">Agents select it by this name, and it starts every conversation’s address.</span><span class="ferr" id="npErr" ${Html.raw(this.error ? '' : 'hidden')}>${this.error}</span></div>
        <div class="field"><span class="lbl">Members</span><div class="pk-slot"></div></div>
        <div class="row row-top"><div class="field grow"><label for="npRet">Retention</label><select class="input" id="npRet">${ret}</select></div>
          <div class="field grow"><label for="npQuota">Quota</label><select class="input" id="npQuota">${quota}</select></div></div>
        <div class="field"><span class="lbl">To put an agent to work on it</span><span class="help">Nothing to configure. Tell the agent “check cafard, project <span class="np-hint">speedtest</span>”, or add <code>cafard project: <span class="np-hint">speedtest</span></code> to the repo’s CLAUDE.md.</span></div>
      </form>
      <div class="m-foot"><button class="btn tonal" data-act="close">Cancel</button><button class="btn filled" data-act="create">Create project</button></div>`;
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
      this.find('#npName')?.classList.add('err');
      return;
    }
    this.app.layer.close();
    this.app.go('projects');
    await this.app.refresh();
    Toaster.show(`${name} is ready.`);
  }
}
