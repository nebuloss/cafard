import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { Avatar, Meter, Switch } from '@shared/widgets';
import type { AdminApp } from './app';
import { GiveScreen } from './create';
import { pathOf } from './route';
import { AdminStore } from './store';
import type { Link, Member, Project, Signin } from './types';
import { PickerDialog } from './pickers';
import { View } from './view';

/**
 * Base of a member's and a project's pages: the item's head, then the page
 * the path names, in sections. The item's own path is its overview; the rail
 * lists its other pages (route.ts).
 */
abstract class ItemPage extends View {
  /** A removal waiting for its confirmation. */
  protected confirming = false;

  protected itemHead(avatar: Html, title: Html, sub: string): Html {
    return html`<div class="item-head">${avatar}<div class="t"><h1>${title}</h1><p>${sub}</p></div></div>`;
  }

  protected gone(kind: string, name: string, back: string): Html {
    return html`<div class="empty"><p>There is no ${kind} called ${name}: it was removed, or the link is wrong.</p><p><a href="${back}">Back to the list</a></p></div>`;
  }

  protected confirmActions(): Record<string, () => void> {
    return {
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

/** A member's pages: /members/<name>, and its projects, access and advanced pages. */
export class MemberPage extends ItemPage {
  private signins: Signin[] | null = null;

  constructor(app: AdminApp, private readonly name: string, private readonly page: string) {
    super(app);
    const path = () => `/members/${AdminStore.path(this.name)}`;
    this.actions = {
      ...this.confirmActions(),
      pause: async () => {
        const m = this.member();
        if (m) await this.app.change(() => this.store.api.patch(path(), { enabled: !m.enabled }), m.enabled ? `${m.name} is paused.` : `${m.name} works again.`);
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
        if (m) this.app.layer.open(PickerDialog.forMember(this.app, m), 'center');
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
        const ok = await this.app.attempt(() => this.store.api.delete(path()), `${this.name} is removed. Their messages stay.`);
        if (ok !== null) await this.app.go('/members');
      },
    };
  }

  private member(): Member | undefined {
    return this.store.member(this.name);
  }

  protected view(): Html {
    const m = this.member();
    if (!m) return this.gone('member', this.name, '/members');
    const kindTxt = m.kind === 'person' ? 'Person' : m.kind === 'agent' ? 'Agent · connects with its token' : 'Device · sends only';
    const head = this.itemHead(Avatar.of(m), html`${m.display_name || m.name}${!m.enabled ? html`<span class="pill muted">paused</span>` : ''}`, `${kindTxt} · @${m.name}`);
    let body: Html[];
    switch (this.page) {
      case 'projects':
        body = [this.projectsSection(m)];
        break;
      case 'access':
        body = [this.accessSection(m)];
        break;
      case 'advanced':
        body = this.advancedSections(m);
        break;
      default:
        body = this.overviewSections(m);
    }
    return html`${head}<div class="secs">${body}</div>`;
  }

  private overviewSections(m: Member): Html[] {
    const out: Html[] = [];
    if (m.kind !== 'device' && !m.joined) out.push(this.linkSection(m));
    if (m.kind !== 'person') {
      out.push(html`<div class="sec"><h3>${m.kind === 'agent' ? 'What this machine can do' : 'Note'}</h3><div class="field"><textarea class="input" id="desc" aria-label="Description">${m.description ?? ''}</textarea>${m.kind === 'agent' ? html`<span class="help">Agents read this, so they know whom to ask: "serial console of the be98 test unit; can flash it".</span>` : ''}</div><div class="row"><button class="btn tonal sm" data-act="save-desc">Save</button></div></div>`);
    }
    const base = pathOf('members', m.name);
    const projects = m.projects.length ? m.projects.map((p) => html`<a class="chip" href="${pathOf('projects', p)}">${p}</a>`) : html`<span class="help">In no project: it can reach nothing.</span>`;
    out.push(html`<div class="sec"><h3>At a glance</h3><dl class="kv">
      <dt>Projects</dt><dd><div class="chips">${projects}</div></dd>
      <dt>${m.kind === 'person' ? 'Signed in' : 'Last used'}</dt><dd>${m.joined || m.kind === 'device' ? html`${time.ago(m.last_used)} <span class="mono">${m.last_from ?? ''}</span>` : 'Not joined yet'}</dd>
      <dt>State</dt><dd>${m.enabled ? 'Working' : 'Paused'}</dd></dl>
      <div class="row"><a class="btn tonal sm" href="${base}/projects">Projects</a><a class="btn tonal sm" href="${base}/access">${m.kind === 'person' ? 'Sign-ins' : 'Token'}</a></div></div>`);
    return out;
  }

  private projectsSection(m: Member): Html {
    const chips = m.projects.map((p) => html`<span class="chip"><a href="${pathOf('projects', p)}">${p}</a><button data-act="rm-proj" data-p="${p}" aria-label="Remove from ${p}">${Icon.of('close')}</button></span>`);
    return html`<div class="sec"><h3>Projects</h3><p class="help">${m.kind === 'device' ? 'It sends to these projects, and reads nothing.' : 'It sees every conversation in these projects.'}</p><div class="chips">${chips.length ? chips : html`<span class="help">In no project: it can reach nothing.</span>`}</div><div class="row"><button class="btn tonal sm" data-act="manage-projects">Add or remove projects</button></div></div>`;
  }

  private accessSection(m: Member): Html {
    if (m.kind === 'person') {
      if (!m.joined) return this.linkSection(m);
      return html`<div class="sec"><h3>Sign-ins</h3><div class="signins">${this.signinRows()}</div><div class="row"><button class="btn tonal sm" data-act="relink">New password link</button></div><span class="help">The link sets a new password and ends every sign-in. You never see the password.</span></div>`;
    }
    return html`<div class="sec"><h3>Token</h3><dl class="kv"><dt>Last used</dt><dd>${m.joined ? time.ago(m.last_used) : 'never'}</dd><dt>From</dt><dd class="mono">${m.last_from || '-'}</dd></dl>
      <div class="row">${m.kind === 'agent' ? html`<button class="btn tonal sm" data-act="relink">New install link</button>` : html`<button class="btn tonal sm" data-act="regen">Regenerate token</button>`}</div>
      <span class="help">${m.kind === 'agent' ? "Running the new link on the machine replaces the token; the old one stops working then. An agent's token is never shown." : 'The current token stops working at once.'}</span></div>`;
  }

  private advancedSections(m: Member): Html[] {
    return [
      html`<div class="sec"><h3>Pause</h3><div class="line"><div class="m"><b>Paused</b><span>Stops working until switched back. Nothing is lost.</span></div>${Switch.of(!m.enabled, 'pause', m.name, 'Paused')}</div></div>`,
      html`<div class="sec"><h3>Remove</h3>${this.confirming
        ? html`<div class="confirm"><p><b>Remove ${m.name} from cafard?</b></p><ul>
            <li>${m.kind === 'person' ? 'Every sign-in ends now.' : 'Its token stops working on the next request.'}</li>
            <li>It leaves its ${m.projects.length} project${m.projects.length === 1 ? '' : 's'}.</li>
            <li>Its messages stay, shown as “${m.name} (removed)”, and the name stays taken.</li></ul>
            <div class="row"><button class="btn danger sm" data-act="delete">Remove ${m.name}</button><button class="btn tonal sm" data-act="no-delete">Cancel</button></div></div>`
        : html`<div class="row"><button class="btn danger-text sm" data-act="ask-delete">Remove ${m.name}…</button></div>`}</div>`,
    ];
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
    return html`${this.signins.map((s) => html`<div class="line"><div class="m"><b>${s.what}</b><span>chat app · ${s.from} · ${time.ago(s.last_used)}</span></div><button class="btn sm outline" data-act="signout-one" data-i="${String(s.id)}">Sign out</button></div>`)}`;
  }

  protected override afterRender(): void {
    super.afterRender();
    const m = this.member();
    if (this.page === 'access' && m?.kind === 'person' && m.joined && this.signins === null) {
      void this.app.attempt(() => this.store.api.get<{ signins: Signin[] }>(`/members/${AdminStore.path(m.name)}/signins`)).then((r) => {
        this.signins = r?.signins ?? [];
        const box = this.find('.signins');
        if (box) box.innerHTML = this.signinRows().value;
      });
    }
  }
}

/** A project's pages: /projects/<name>, and its members, files and advanced pages. */
export class ProjectPage extends ItemPage {
  constructor(app: AdminApp, private readonly name: string, private readonly page: string) {
    super(app);
    const path = () => `/projects/${AdminStore.path(this.name)}`;
    this.actions = {
      ...this.confirmActions(),
      'manage-members': () => {
        const p = this.project();
        if (p) this.app.layer.open(PickerDialog.forProject(this.app, p), 'center');
      },
      archive: async () => {
        const p = this.project();
        if (p) await this.app.change(() => this.store.api.patch(path(), { archived: !p.archived }), p.archived ? `${p.name} is open again.` : `${p.name} is archived and read-only.`);
      },
      'p-delete': async () => {
        const ok = await this.app.attempt(() => this.store.api.delete(path(), { confirm: this.$<HTMLInputElement>('#pName').value.trim() }), `${this.name} is deleted.`);
        if (ok !== null) await this.app.go('/projects');
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
    if (!p) return this.gone('project', this.name, '/projects');
    const head = this.itemHead(Avatar.project(p.name), html`${p.name}${p.archived ? html`<span class="pill muted">archived · read-only</span>` : ''}`,
      `${p.conversations} conversations · ${p.files} files · ${p.members.length} members`);
    let body: Html[];
    switch (this.page) {
      case 'members':
        body = [this.membersSection(p, p.members.length)];
        break;
      case 'files':
        body = [this.filesSection(p)];
        break;
      case 'advanced':
        body = this.advancedSections(p);
        break;
      default:
        body = this.overviewSections(p);
    }
    return html`${head}<div class="secs">${body}</div>`;
  }

  private overviewSections(p: Project): Html[] {
    const base = pathOf('projects', p.name);
    return [
      html`<div class="sec"><h3>At a glance</h3><dl class="kv">
        <dt>Conversations</dt><dd>${p.conversations}</dd>
        <dt>Files</dt><dd>${p.files}, ${Bytes.format(p.bytes)} of ${Bytes.format(p.quota_bytes)}</dd>
        <dt>Retention</dt><dd>${p.retention_days} days</dd>
        <dt>State</dt><dd>${p.archived ? 'Archived: readable, nothing can be posted' : 'Open'}</dd></dl>
        <div class="row"><a class="btn tonal sm" href="${base}/members">Members</a><a class="btn tonal sm" href="${base}/files">Files</a></div></div>`,
      this.membersSection(p, 6),
    ];
  }

  private membersSection(p: Project, shown: number): Html {
    const count = (k: string) => p.members.filter((m) => m.kind === k).length;
    const rows = p.members.slice(0, shown).map((m) => html`<a class="line" href="${pathOf('members', m.name)}">${Avatar.of(m, 'sm')}<div class="m"><b>${m.display_name || m.name}</b><span>${m.kind === 'person' ? 'Person' : m.kind === 'agent' ? 'Agent · reads and writes' : 'Device · sends only'}</span></div><span class="chev">${Icon.of('chev')}</span></a>`);
    return html`<div class="sec"><h3>Members · ${count('person')} people, ${count('agent')} agents, ${count('device')} devices</h3>${rows.length ? rows : html`<span class="help">Nobody yet.</span>`}
      ${p.members.length > shown ? html`<a class="more-line" href="${pathOf('projects', p.name, 'members')}">and ${p.members.length - shown} more</a>` : ''}
      ${p.archived ? html`<span class="help">Archived: members cannot change until it is unarchived.</span>` : html`<div class="row"><button class="btn tonal sm" data-act="manage-members">Add or remove members</button></div>`}</div>`;
  }

  private filesSection(p: Project): Html {
    const option = (v: number, label: string, current: number) => html`<option value="${String(v)}" ${Html.raw(v === current ? 'selected' : '')}>${label}</option>`;
    const days = [7, 14, 30, 60, 90, 365];
    if (!days.includes(p.retention_days)) days.push(p.retention_days);
    const quotas = [1, 2, 5, 10, 20, 50].map((g) => g * 2 ** 30);
    if (!quotas.includes(p.quota_bytes)) quotas.push(p.quota_bytes);
    return html`<div class="sec"><h3>Files</h3>${Meter.of(p)}
      <div class="field"><label for="pRet">Retention</label><select class="input" id="pRet">${days.sort((a, b) => a - b).map((d) => option(d, `${d} days`, p.retention_days))}</select><span class="help">Files older than this expire; messages stay.</span></div>
      <div class="field"><label for="pQuota">Quota</label><select class="input" id="pQuota">${quotas.sort((a, b) => a - b).map((b) => option(b, Bytes.format(b), p.quota_bytes))}</select></div></div>`;
  }

  private advancedSections(p: Project): Html[] {
    return [
      html`<div class="sec"><h3>${p.archived ? 'Archived' : 'Archive'}</h3><p class="help">${p.archived ? 'Everything is readable and nothing can be posted.' : 'Keeps everything readable and stops all posting. You can undo it.'}</p><div class="row"><button class="btn tonal sm" data-act="archive">${p.archived ? 'Unarchive' : 'Archive project'}</button></div></div>`,
      html`<div class="sec"><h3>Delete</h3>${this.confirming
        ? html`<div class="confirm"><p><b>This cannot be undone.</b> Deleting ${p.name} removes, for every member:</p><ul><li>${p.conversations} conversations and their messages</li><li>${p.files} files, ${Bytes.format(p.bytes)}</li><li>${p.members.length} memberships</li></ul>
            <div class="field"><label for="pName">Type <b>${p.name}</b> to confirm</label><input class="input" id="pName" autocomplete="off"></div>
            <div class="row"><button class="btn danger sm" id="pDel" data-act="p-delete" disabled>Delete ${p.name}</button><button class="btn tonal sm" data-act="no-delete">Cancel</button></div></div>`
        : html`<div class="row"><button class="btn danger-text sm" data-act="ask-delete">Delete project…</button></div>`}</div>`,
    ];
  }
}
