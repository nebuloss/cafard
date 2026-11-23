import { Component } from '@shared/component';
import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { PickerState, PickerView, type PickItem } from '@shared/picker';
import { Avatar, Meter, Switch } from '@shared/widgets';
import type { AdminApp } from './app';
import { GiveScreen } from './create';
import { AdminStore } from './store';
import type { Link, Member, Project, Signin } from './types';

/** The close button of a drawer or a dialog. */
export function closeButton(): Html {
  return html`<button class="icon-btn" data-act="close" aria-label="Close">${Icon.of('close')}</button>`;
}

/** Base of the two drawers: a head with a close button and sections below. */
abstract class Drawer extends Component {
  /** A name whose removal is waiting for its confirmation. */
  protected confirming = false;

  constructor(protected readonly app: AdminApp) {
    super('aside');
  }

  protected get store(): AdminStore {
    return this.app.store;
  }

  protected headOf(avatar: Html, title: Html, sub: string): Html {
    return html`<div class="d-head">${avatar}<div class="t"><h2>${title}</h2><p>${sub}</p></div>${closeButton()}</div>`;
  }

  protected baseActions(): Record<string, (t: HTMLElement) => void | Promise<void>> {
    return {
      close: () => this.app.closeDrawer(),
      'ask-delete': () => {
        this.confirming = true;
        this.render();
      },
      'no-delete': () => {
        this.confirming = false;
        this.render();
      },
    };
  }
}

export class MemberDrawer extends Drawer {
  private signins: Signin[] | null = null;

  constructor(app: AdminApp, private readonly name: string) {
    super(app);
    const path = () => `/members/${AdminStore.path(this.name)}`;
    this.actions = {
      ...this.baseActions(),
      pause: async () => {
        const m = this.member();
        if (m) await this.app.change(() => this.store.api.patch(path(), { enabled: !m.enabled }), m.enabled ? `${m.name} is paused.` : `${m.name} works again.`);
      },
      admin: async () => {
        const m = this.member();
        if (m) await this.app.change(() => this.store.api.patch(path(), { admin: !m.admin }));
      },
      'save-desc': async () => {
        await this.app.change(() => this.store.api.patch(path(), { description: this.$<HTMLTextAreaElement>('#desc').value }), 'Saved.');
      },
      'rm-proj': async (t) => {
        const m = this.member();
        const p = t.dataset['p'] ?? '';
        if (m) await this.app.change(() => this.store.api.patch(path(), { projects: m.projects.filter((x) => x !== p) }), `Removed from ${p}.`);
      },
      'manage-projects': () => {
        const m = this.member();
        if (m) this.app.layer2.open(PickerDialog.forMember(this.app, m), 'center');
      },
      relink: async () => {
        const r = await this.app.attempt(() => this.store.api.post<{ link: Link }>(`${path()}/link`, { hours: 24 }));
        await this.app.refresh();
        const m = this.member();
        if (r && m) this.app.layer.open(new GiveScreen(this.app, m, r.link), 'center');
      },
      regen: async () => {
        const r = await this.app.attempt(() => this.store.api.post<{ token: string }>(`${path()}/token`));
        await this.app.refresh();
        const m = this.member();
        if (r && m) this.app.layer.open(new GiveScreen(this.app, m, undefined, r.token), 'center');
      },
      'signout-one': async (t) => {
        await this.app.attempt(() => this.store.api.delete(`${path()}/signins/${t.dataset['i']}`), 'Signed out.');
        this.signins = null;
        this.render();
      },
      delete: async () => {
        await this.app.attempt(() => this.store.api.delete(path()), `${this.name} is removed. Their messages stay.`);
        this.app.closeDrawer();
        await this.app.refresh();
      },
    };
  }

  private member(): Member | undefined {
    return this.store.member(this.name);
  }

  protected view(): Html {
    const m = this.member();
    if (!m) return html`<div class="d-body"><p class="empty">${this.name} is gone.</p></div>`;
    const me = m.name === this.store.me?.name;
    const kindTxt = m.kind === 'person' ? (m.admin ? 'Person · admin' : 'Person') : m.kind === 'agent' ? 'Agent · connects with its token' : 'Device · sends only';
    const sections: Html[] = [];

    if (m.kind !== 'device' && !m.joined) sections.push(this.linkSection(m));
    if (m.kind !== 'person') {
      sections.push(html`<div class="sec"><h3>${m.kind === 'agent' ? 'What this machine can do' : 'Note'}</h3><div class="field"><textarea class="input" id="desc" aria-label="Description">${m.description ?? ''}</textarea>${m.kind === 'agent' ? html`<span class="help">Agents read this, so they know whom to ask: "serial console of the be98 test unit; can flash it".</span>` : ''}</div><div class="row"><button class="btn tonal sm" data-act="save-desc">Save</button></div></div>`);
    } else {
      sections.push(html`<div class="sec"><h3>Account</h3><div class="line"><div class="m"><b>Admin</b><span>Can sign in here and change everything on this page.</span></div>${me ? html`<span class="help">you</span>` : Switch.of(!!m.admin, 'admin', m.name, 'Admin')}</div></div>`);
    }
    const chips = m.projects.map((p) => html`<span class="chip">${p}<button data-act="rm-proj" data-p="${p}" aria-label="Remove from ${p}">${Icon.of('close')}</button></span>`);
    sections.push(html`<div class="sec"><h3>Projects</h3><div class="chips">${chips.length ? chips : html`<span class="help">In no project: it can reach nothing.</span>`}</div><div class="row"><button class="btn tonal sm" data-act="manage-projects">Add or remove projects</button></div></div>`);
    if (m.kind === 'person' && m.joined) {
      sections.push(html`<div class="sec"><h3>Sign-ins</h3><div class="signins">${this.signinRows()}</div><div class="row"><button class="btn tonal sm" data-act="relink">New password link</button></div><span class="help">The link sets a new password and ends every sign-in. You never see the password.</span></div>`);
    }
    if (m.kind !== 'person') {
      sections.push(html`<div class="sec"><h3>Token</h3><dl class="kv"><dt>Last used</dt><dd>${m.joined ? time.ago(m.last_used) : 'never'}</dd><dt>From</dt><dd class="mono">${m.last_from || '-'}</dd></dl>
        <div class="row">${m.kind === 'agent' ? html`<button class="btn tonal sm" data-act="relink">New install link</button>` : html`<button class="btn tonal sm" data-act="regen">Regenerate token</button>`}</div>
        <span class="help">${m.kind === 'agent' ? "Running the new link on the machine replaces the token; the old one stops working then. An agent's token is never shown." : 'The current token stops working at once.'}</span></div>`);
    }
    if (!me) {
      sections.push(html`<div class="sec"><div class="line"><div class="m"><b>Paused</b><span>Stops working until switched back. Nothing is lost.</span></div>${Switch.of(!m.enabled, 'pause', m.name, 'Paused')}</div></div>`);
      sections.push(html`<div class="sec"><h3>Remove</h3>${this.confirming
        ? html`<div class="confirm"><p><b>Remove ${m.name} from cafard?</b></p><ul>
            <li>${m.kind === 'person' ? 'Every sign-in ends now.' : 'Its token stops working on the next request.'}</li>
            <li>It leaves its ${m.projects.length} project${m.projects.length === 1 ? '' : 's'}.</li>
            <li>Its messages stay, shown as “${m.name} (removed)”, and the name stays taken.</li></ul>
            <div class="row"><button class="btn danger sm" data-act="delete">Remove ${m.name}</button><button class="btn tonal sm" data-act="no-delete">Cancel</button></div></div>`
        : html`<div class="row"><button class="btn danger-text sm" data-act="ask-delete">Remove ${m.name}…</button></div>`}</div>`);
    }
    return html`${this.headOf(Avatar.of(m), html`${m.display_name || m.name}${!m.enabled ? html`<span class="pill muted">paused</span>` : ''}`, `${kindTxt} · @${m.name}`)}<div class="d-body">${sections}</div>`;
  }

  private linkSection(m: Member): Html {
    const l = m.link;
    const what = m.kind === 'agent' ? 'install' : 'join';
    const msg = !l ? 'No link yet.'
      : l.state === 'waiting' ? `The ${what} link is waiting to be used · ${time.until(l.expires_at)}.`
      : l.state === 'expired' ? 'The link expired before it was used.'
      : 'The link was cancelled.';
    return html`<div class="sec"><div class="status">${Icon.of('info')}${msg}</div><div class="row"><button class="btn filled sm" data-act="relink">Make a new ${what} link</button></div></div>`;
  }

  private signinRows(): Html {
    if (!this.signins) return html`<p class="help">Loading…</p>`;
    if (!this.signins.length) return html`<p class="help">Not signed in anywhere.</p>`;
    return html`${this.signins.map((s) => html`<div class="line"><div class="m"><b>${s.what}${s.current ? ' · this one' : ''}</b><span>${s.surface === 'admin' ? 'admin UI' : 'chat app'} · ${s.from} · ${time.ago(s.last_used)}</span></div>${s.current ? '' : html`<button class="btn sm outline" data-act="signout-one" data-i="${String(s.id)}">Sign out</button>`}</div>`)}`;
  }

  protected override afterRender(): void {
    super.afterRender();
    const m = this.member();
    if (m?.kind === 'person' && m.joined && this.signins === null) {
      void this.app.attempt(() => this.store.api.get<{ signins: Signin[] }>(`/members/${AdminStore.path(m.name)}/signins`)).then((r) => {
        this.signins = r?.signins ?? [];
        const box = this.find('.signins');
        if (box) box.innerHTML = this.signinRows().value;
      });
    }
  }

  override render(): this {
    // Sign-ins are read again each time the drawer is drawn from fresh data.
    this.signins = null;
    return super.render();
  }
}

export class ProjectDrawer extends Drawer {
  constructor(app: AdminApp, private readonly name: string) {
    super(app);
    const path = () => `/projects/${AdminStore.path(this.name)}`;
    this.actions = {
      ...this.baseActions(),
      'manage-members': () => {
        const p = this.project();
        if (p) this.app.layer2.open(PickerDialog.forProject(this.app, p), 'center');
      },
      archive: async () => {
        const p = this.project();
        if (p) await this.app.change(() => this.store.api.patch(path(), { archived: !p.archived }), p.archived ? `${p.name} is open again.` : `${p.name} is archived and read-only.`);
      },
      'p-delete': async () => {
        await this.app.attempt(() => this.store.api.delete(path(), { confirm: this.$<HTMLInputElement>('#pName').value.trim() }), `${this.name} is deleted.`);
        this.app.closeDrawer();
        await this.app.refresh();
      },
    };
    this.root.addEventListener('change', (e) => {
      const t = e.target as HTMLSelectElement;
      if (t.id === 'pRet') void this.app.change(() => this.store.api.patch(path(), { retention_days: Number(t.value) }), 'Retention changed.');
      if (t.id === 'pQuota') void this.app.change(() => this.store.api.patch(path(), { quota_bytes: Number(t.value) }), 'Quota changed.');
    });
    this.root.addEventListener('input', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.id === 'pName') this.$<HTMLButtonElement>('#pDel').disabled = t.value.trim() !== this.name;
    });
  }

  private project(): Project | undefined {
    return this.store.project(this.name);
  }

  protected view(): Html {
    const p = this.project();
    if (!p) return html`<div class="d-body"><p class="empty">${this.name} is gone.</p></div>`;
    const count = (k: string) => p.members.filter((m) => m.kind === k).length;
    const option = (v: number, label: string, current: number) => html`<option value="${String(v)}" ${Html.raw(v === current ? 'selected' : '')}>${label}</option>`;
    const days = [7, 14, 30, 60, 90, 365];
    if (!days.includes(p.retention_days)) days.push(p.retention_days);
    const quotas = [1, 2, 5, 10, 20, 50].map((g) => g * 2 ** 30);
    if (!quotas.includes(p.quota_bytes)) quotas.push(p.quota_bytes);
    const members = p.members.slice(0, 6).map((m) => html`<div class="line">${Avatar.of(m, 'sm')}<div class="m"><b>${m.display_name || m.name}</b><span>${m.kind === 'person' ? 'Person' : m.kind === 'agent' ? 'Agent · reads and writes' : 'Device · sends only'}</span></div></div>`);
    return html`${this.headOf(Avatar.project(p.name), html`${p.name}${p.archived ? html`<span class="pill muted">archived · read-only</span>` : ''}`, `${p.conversations} conversations · ${p.files} files`)}<div class="d-body">
      <div class="sec"><h3>Members · ${count('person')} people, ${count('agent')} agents, ${count('device')} devices</h3>${members.length ? members : html`<span class="help">Nobody yet.</span>`}${p.members.length > 6 ? html`<span class="more-line">and ${p.members.length - 6} more</span>` : ''}
        ${p.archived ? html`<span class="help">Archived: members cannot change until it is unarchived.</span>` : html`<div class="row"><button class="btn tonal sm" data-act="manage-members">Add or remove members</button></div>`}</div>
      <div class="sec"><h3>Files</h3>${Meter.of(p)}
        <div class="field"><label for="pRet">Retention</label><select class="input" id="pRet">${days.sort((a, b) => a - b).map((d) => option(d, `${d} days`, p.retention_days))}</select><span class="help">Files older than this expire; messages stay.</span></div>
        <div class="field"><label for="pQuota">Quota</label><select class="input" id="pQuota">${quotas.sort((a, b) => a - b).map((b) => option(b, Bytes.format(b), p.quota_bytes))}</select></div></div>
      <div class="sec"><h3>${p.archived ? 'Archived' : 'Archive'}</h3><p class="help">${p.archived ? 'Everything is readable and nothing can be posted.' : 'Keeps everything readable and stops all posting. You can undo it.'}</p><div class="row"><button class="btn tonal sm" data-act="archive">${p.archived ? 'Unarchive' : 'Archive project'}</button></div></div>
      <div class="sec"><h3>Delete</h3>${this.confirming
        ? html`<div class="confirm"><p><b>This cannot be undone.</b> Deleting ${p.name} removes, for every member:</p><ul><li>${p.conversations} conversations and their messages</li><li>${p.files} files, ${Bytes.format(p.bytes)}</li><li>${p.members.length} memberships</li></ul>
            <div class="field"><label for="pName">Type <b>${p.name}</b> to confirm</label><input class="input" id="pName" autocomplete="off"></div>
            <div class="row"><button class="btn danger sm" id="pDel" data-act="p-delete" disabled>Delete ${p.name}</button><button class="btn tonal sm" data-act="no-delete">Cancel</button></div></div>`
        : html`<div class="row"><button class="btn danger-text sm" data-act="ask-delete">Delete project…</button></div>`}</div></div>`;
  }
}

/** Items for the member picker, from the store. */
export function memberItems(members: Member[]): PickItem[] {
  return members.map((m) => ({
    id: m.name,
    label: m.display_name || m.name,
    kind: m.kind,
    avatar: Avatar.of(m, 'sm'),
    text: [m.name, m.display_name, m.description].join(' ').toLowerCase(),
    sub: m.kind === 'person' ? `@${m.name}${m.projects.length ? ' · ' + m.projects.join(', ') : ''}`
      : m.kind === 'agent' ? m.description || 'agent' : `Sends only${m.description ? ' · ' + m.description : ''}`,
  }));
}

/** Items for the project picker, from the store. */
export function projectItems(projects: Project[], current: Set<string>): PickItem[] {
  return projects.map((p) => ({
    id: p.name,
    label: p.name,
    avatar: Avatar.project(p.name, 'sm'),
    text: p.name,
    disabled: !!p.archived && !current.has(p.name),
    sub: p.archived ? 'Archived, read-only: nobody can be added' : `${p.conversations} conversations · ${p.members.length} members`,
  }));
}

/**
 * PickerDialog is "Add or remove members" on a project and "Add or remove
 * projects" on a member: one picker, saved as the full set, which the server
 * applies one change at a time through access.
 */
export class PickerDialog extends Component {
  private readonly picker: PickerView;

  private constructor(
    private readonly app: AdminApp,
    private readonly title: string,
    private readonly sub: string,
    state: PickerState,
    kinds: boolean,
    private readonly save: (selected: string[]) => Promise<unknown>,
  ) {
    super('div');
    this.picker = new PickerView(state, { kinds, inline: false, placeholder: kinds ? 'Search by name or what it does' : 'Search projects', onChange: () => this.updateSave() });
    this.actions = {
      close: () => this.app.layer2.close(),
      'pk-save': async () => {
        const { add, remove } = state.changes();
        this.app.layer2.close();
        await this.app.change(() => this.save([...state.selected]), `Saved: ${add.length} added, ${remove.length} removed.`);
      },
    };
  }

  static forProject(app: AdminApp, p: Project): PickerDialog {
    const current = p.members.map((m) => m.name);
    return new PickerDialog(app, `Members of ${p.name}`, `Tick to add, untick to remove. People and agents see every conversation in ${p.name}; devices only send to it.`,
      new PickerState(memberItems(app.store.members), current, current), true,
      (members) => app.store.api.patch(`/projects/${AdminStore.path(p.name)}`, { members }));
  }

  static forMember(app: AdminApp, m: Member): PickerDialog {
    return new PickerDialog(app, `Projects of ${m.display_name || m.name}`, m.kind === 'device' ? 'It can send to the ticked projects, and read nothing.' : 'It sees every conversation in the ticked projects.',
      new PickerState(projectItems(app.store.projects, new Set(m.projects)), m.projects, m.projects), false,
      (projects) => app.store.api.patch(`/members/${AdminStore.path(m.name)}`, { projects }));
  }

  protected view(): Html {
    return html`<div class="m-head"><div class="t"><h2>${this.title}</h2><p>${this.sub}</p></div>${closeButton()}</div>
      <div class="m-body"><div class="pk-slot"></div></div>
      <div class="m-foot"><button class="btn tonal" data-act="close">Cancel</button><button class="btn filled" id="pkSave" data-act="pk-save" disabled>Save</button></div>`;
  }

  protected override afterRender(): void {
    this.$('.pk-slot').replaceWith(this.picker.root);
    this.picker.render();
    this.picker.focus();
  }

  private updateSave(): void {
    const btn = this.find<HTMLButtonElement>('#pkSave');
    if (!btn) return;
    const label = this.picker.state.saveLabel();
    btn.disabled = label === 'Save';
    btn.textContent = label;
  }
}
