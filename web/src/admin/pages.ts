import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { ui } from '@shared/ui';
import { Avatar, Meter, Switch } from '@shared/widgets';
import type { AdminApp } from './app';
import { GiveScreen } from './create';
import { pathOf } from './route';
import { AdminStore } from './store';
import type { Link, Member, Project, Signin } from './types';
import { PickerDialog } from './pickers';
import { View } from './view';

/**
 * Base of a member's and a project's pages: the item's head, then its
 * cards, side by side.
 */
abstract class ItemPage extends View {
  /** A removal waiting for its confirmation. */
  protected confirming = false;

  protected itemHead(avatar: Html, title: Html, sub: string): Html {
    return html`<div class="flex items-center gap-3">${avatar}<div class="min-w-0"><h1 class="flex flex-wrap items-center gap-2 text-xl font-semibold">${title}</h1><p class="text-[13px] text-on-surface-variant">${sub}</p></div></div>`;
  }

  protected gone(kind: string, name: string, back: string): Html {
    return html`<div class="${ui.empty}"><p>There is no ${kind} called ${name}: it was removed, or the link is wrong.</p><p><a href="${back}">Back to the list</a></p></div>`;
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

/** A member's page, /members/<name>: everything about it, in cards. */
export class MemberPage extends ItemPage {
  private signins: Signin[] | null = null;

  constructor(app: AdminApp, private readonly name: string) {
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
    const head = this.itemHead(Avatar.of(m), html`${m.display_name || m.name}${!m.enabled ? html`<span class="${ui.pill.muted}">paused</span>` : ''}`, `${kindTxt} · @${m.name}`);
    return html`${head}${this.layout({ side: this.overviewSections(m), main: [this.projectsSection(m), this.accessSection(m)], end: this.advancedSections(m) })}`;
  }

  private overviewSections(m: Member): Html[] {
    const out: Html[] = [];
    if (m.kind !== 'device' && !m.joined) out.push(this.linkSection(m));
    if (m.kind !== 'person') {
      out.push(html`<div class="${ui.card}"><h2 class="${ui.cardTitle}">${m.kind === 'agent' ? 'What it can do' : 'Note'}</h2><div class="${ui.field}"><textarea class="${ui.textarea}" id="desc" aria-label="Description" placeholder="${m.kind === 'agent' ? 'What this machine can do: serial console of the be98 unit; can flash it' : ''}">${m.description ?? ''}</textarea></div><div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('tonal', 'sm')}" data-act="save-desc">Save</button></div></div>`);
    }
    return out;
  }

  private projectsSection(m: Member): Html {
    const chips = m.projects.map((p) => html`<span class="${ui.chip}"><a href="${pathOf('projects', p)}">${p}</a><button class="grid size-4 place-items-center rounded-full text-on-surface-variant hover:bg-surface-highest [&_.ic]:size-3" data-act="rm-proj" data-p="${p}" aria-label="Remove from ${p}">${Icon.of('close')}</button></span>`);
    return html`<div class="${ui.card}"><h2 class="${ui.cardTitle}">Projects</h2><div class="flex flex-wrap gap-1">${chips.length ? chips : html`<span class="text-[12.5px] text-on-surface-variant">In no project: it can reach nothing.</span>`}</div><div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('tonal', 'sm')}" data-act="manage-projects">Add or remove projects</button></div></div>`;
  }

  private accessSection(m: Member): Html {
    if (m.kind === 'person') {
      if (!m.joined) return this.linkSection(m);
      return html`<div class="${ui.card}"><h2 class="${ui.cardTitle}">Sign-ins</h2><div class="signins">${this.signinRows()}</div><div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('tonal', 'sm')}" data-act="relink" title="Sets a new password and ends every sign-in">New password link</button></div></div>`;
    }
    return html`<div class="${ui.card}"><h2 class="${ui.cardTitle}">Token</h2><dl class="${ui.kv}"><dt>Last used</dt><dd>${m.joined ? time.ago(m.last_used) : 'never'}</dd><dt>From</dt><dd class="font-mono text-xs">${m.last_from || '-'}</dd></dl>
      <div class="flex flex-wrap items-center gap-2.5">${m.kind === 'agent' ? html`<button class="${ui.btn('tonal', 'sm')}" data-act="relink" title="Run on the machine, it replaces the token">New install link</button>` : html`<button class="${ui.btn('tonal', 'sm')}" data-act="regen" title="The current token stops working at once">Regenerate token</button>`}</div></div>`;
  }

  private advancedSections(m: Member): Html[] {
    return [
      html`<div class="${ui.card}"><h2 class="${ui.cardTitle}">Pause or remove</h2><div class="${ui.line}"><div class="m"><b>Paused</b><span>Nothing is lost.</span></div>${Switch.of(!m.enabled, 'pause', m.name, 'Paused')}</div>${this.confirming
        ? html`<div class="${ui.confirm}"><p><b>Remove ${m.name} from cafard?</b></p><ul>
            <li>${m.kind === 'person' ? 'Every sign-in ends now.' : 'Its token stops working on the next request.'}</li>
            <li>It leaves its ${m.projects.length} project${m.projects.length === 1 ? '' : 's'}.</li>
            <li>Its messages stay, shown as “${m.name} (removed)”, and the name stays taken.</li></ul>
            <div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('danger', 'sm')}" data-act="delete">Remove ${m.name}</button><button class="${ui.btn('tonal', 'sm')}" data-act="no-delete">Cancel</button></div></div>`
        : html`<div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('danger-text', 'sm')}" data-act="ask-delete">Remove ${m.name}…</button></div>`}</div>`,
    ];
  }

  private linkSection(m: Member): Html {
    const l = m.link;
    const what = m.kind === 'agent' ? 'install' : 'join';
    const msg = !l ? 'No link yet.'
      : l.state === 'waiting' ? `The ${what} link is waiting to be used · ${time.until(l.expires_at)}.`
      : l.state === 'expired' ? 'The link expired before it was used.'
      : 'The link was cancelled.';
    return html`<div class="${ui.card}"><div class="flex items-center gap-2 rounded-md3-s bg-surface-lowest px-3 py-2.5 text-[13.5px]">${Icon.of('info')}${msg}</div><div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('filled', 'sm')}" data-act="relink">Make a new ${what} link</button></div></div>`;
  }

  private signinRows(): Html {
    if (!this.signins) return html`<p class="text-[12.5px] text-on-surface-variant">Loading…</p>`;
    if (!this.signins.length) return html`<p class="text-[12.5px] text-on-surface-variant">Not signed in anywhere.</p>`;
    return html`${this.signins.map((s) => html`<div class="${ui.line}"><div class="m"><b>${s.what}</b><span>chat app · ${s.from} · ${time.ago(s.last_used)}</span></div><button class="${ui.btn('outlined', 'sm')}" data-act="signout-one" data-i="${String(s.id)}">Sign out</button></div>`)}`;
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
}

/** A project's page, /projects/<name>: everything about it, in cards. */
export class ProjectPage extends ItemPage {
  constructor(app: AdminApp, private readonly name: string) {
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
    const head = this.itemHead(Avatar.project(p.name), html`${p.name}${p.archived ? html`<span class="${ui.pill.muted}">archived · read-only</span>` : ''}`,
      `${p.conversations} conversations · ${p.files} files · ${p.members.length} members`);
    return html`${head}${this.layout({ side: [this.filesSection(p)], main: [this.membersSection(p)], end: this.advancedSections(p) })}`;
  }

  private membersSection(p: Project): Html {
    const count = (k: string) => p.members.filter((m) => m.kind === k).length;
    const rows = p.members.map((m) => html`<a class="${ui.line} state-layer -mx-1 rounded-md3-s p-1 text-inherit no-underline" href="${pathOf('members', m.name)}">${Avatar.of(m, 'sm')}<div class="m"><b>${m.display_name || m.name}</b><span>${m.kind === 'person' ? 'Person' : m.kind === 'agent' ? 'Agent · reads and writes' : 'Device · sends only'}</span></div><span class="ml-auto text-on-surface-variant">${Icon.of('chev')}</span></a>`);
    return html`<div class="${ui.card}"><h2 class="${ui.cardTitle}">Members · ${count('person')} people, ${count('agent')} agents, ${count('device')} devices</h2>${rows.length ? rows : html`<span class="text-[12.5px] text-on-surface-variant">Nobody yet.</span>`}
      ${p.archived ? '' : html`<div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('tonal', 'sm')}" data-act="manage-members">Add or remove members</button></div>`}</div>`;
  }

  private filesSection(p: Project): Html {
    const option = (v: number, label: string, current: number) => html`<option value="${String(v)}" ${Html.raw(v === current ? 'selected' : '')}>${label}</option>`;
    const days = [7, 14, 30, 60, 90, 365];
    if (!days.includes(p.retention_days)) days.push(p.retention_days);
    const quotas = [1, 2, 5, 10, 20, 50].map((g) => g * 2 ** 30);
    if (!quotas.includes(p.quota_bytes)) quotas.push(p.quota_bytes);
    return html`<div class="${ui.card}"><h2 class="${ui.cardTitle}">Files</h2>${Meter.of(p)}
      <div class="${ui.field}"><label class="${ui.label}" for="pRet">Retention</label><select class="${ui.input}" id="pRet">${days.sort((a, b) => a - b).map((d) => option(d, `${d} days`, p.retention_days))}</select></div>
      <div class="${ui.field}"><label class="${ui.label}" for="pQuota">Quota</label><select class="${ui.input}" id="pQuota">${quotas.sort((a, b) => a - b).map((b) => option(b, Bytes.format(b), p.quota_bytes))}</select></div></div>`;
  }

  private advancedSections(p: Project): Html[] {
    return [
      html`<div class="${ui.card}"><h2 class="${ui.cardTitle}">Archive or delete</h2><div class="${ui.line}"><div class="m"><b>${p.archived ? 'Archived' : 'Archive'}</b><span>${p.archived ? 'Readable; nothing can be posted.' : 'Readable; stops all posting. Can be undone.'}</span></div><button class="${ui.btn('tonal', 'sm')}" data-act="archive">${p.archived ? 'Unarchive' : 'Archive'}</button></div>${this.confirming
        ? html`<div class="${ui.confirm}"><p><b>This cannot be undone.</b> Deleting ${p.name} removes, for every member:</p><ul><li>${p.conversations} conversations and their messages</li><li>${p.files} files, ${Bytes.format(p.bytes)}</li><li>${p.members.length} memberships</li></ul>
            <div class="${ui.field}"><label class="${ui.label}" for="pName">Type <b>${p.name}</b> to confirm</label><input class="${ui.input}" id="pName" autocomplete="off"></div>
            <div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('danger', 'sm')}" id="pDel" data-act="p-delete" disabled>Delete ${p.name}</button><button class="${ui.btn('tonal', 'sm')}" data-act="no-delete">Cancel</button></div></div>`
        : html`<div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('danger-text', 'sm')}" data-act="ask-delete">Delete project…</button></div>`}</div>`,
    ];
  }
}
