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
 * Base of a member's and a project's pages: its header with its state on the
 * right, a row of figures, then its cards one under another, what cannot be
 * undone last.
 */
abstract class ItemPage extends View {
  /** A removal waiting for its confirmation. */
  protected confirming = false;

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
    const kindTxt = m.kind === 'person' ? 'Person' : m.kind === 'agent' ? 'Agent' : 'Device · sends only';
    const state = !m.enabled
      ? html`<span class="${ui.pill.muted}">paused</span>`
      : m.kind !== 'device' && !m.joined ? html`<span class="${ui.pill.warn}">not joined</span>` : html`<span class="${ui.pill.ok}">active</span>`;
    const head = this.itemHead(Avatar.of(m), m.display_name || m.name, `${kindTxt} · @${m.name}`,
      html`${state}${Switch.of(m.enabled, 'pause', m.name, m.enabled ? `Pause ${m.name}` : `Resume ${m.name}`)}`);
    const used = m.joined || m.kind === 'device' ? time.ago(m.last_used) : 'never';
    const stats = this.stats([
      { label: 'Projects', value: m.projects.length, hint: m.projects.length ? m.projects.slice(0, 3).join(', ') + (m.projects.length > 3 ? '…' : '') : 'reaches nothing' },
      { label: m.kind === 'person' ? 'Last sign-in' : 'Last used', value: used, hint: m.last_from ?? '' },
      m.kind === 'person'
        ? { label: 'Sign-ins', value: m.signins ?? 0, hint: 'chat app' }
        : { label: 'Token', value: m.joined ? 'in use' : m.kind === 'agent' ? 'not yet' : 'issued', hint: m.kind === 'agent' ? 'from its install link' : 'shown once' },
    ]);
    return html`${head}${stats}${this.linkCard(m)}${this.descriptionCard(m)}${this.projectsCard(m)}${this.accessCard(m)}${this.removeCard(m)}`;
  }

  /** A person or an agent that has not joined: its link, and a new one. */
  private linkCard(m: Member): Html {
    if (m.kind === 'device' || m.joined) return Html.empty;
    const l = m.link;
    const what = m.kind === 'agent' ? 'install' : 'sign-in';
    const msg = !l ? 'No link yet.'
      : l.state === 'waiting' ? `The ${what} link is waiting · ${time.until(l.expires_at)}.`
      : l.state === 'expired' ? 'The link expired before it was used.'
      : 'The link was cancelled.';
    return this.card(m.kind === 'agent' ? 'Install link' : 'Sign-in link', html`<p class="text-sm text-on-surface-variant">${msg}</p>`,
      html`<button class="${ui.btn('filled', 'sm')}" data-act="relink">New ${what} link</button>`);
  }

  private descriptionCard(m: Member): Html {
    if (m.kind === 'person') return Html.empty;
    return this.card(m.kind === 'agent' ? 'What it can do' : 'Note',
      html`<textarea class="${ui.textarea}" id="desc" aria-label="Description" placeholder="${m.kind === 'agent' ? 'Serial console of the be98 unit; can flash it' : ''}">${m.description ?? ''}</textarea>`,
      html`<button class="${ui.btn('tonal', 'sm')}" data-act="save-desc">Save</button>`);
  }

  private projectsCard(m: Member): Html {
    const rows = m.projects.map((name) => {
      const p = this.store.project(name);
      return html`<tr class="${ui.tr}"><td class="${ui.td}"><a class="flex items-center gap-2 font-semibold text-inherit no-underline hover:underline" href="${pathOf('projects', name)}">${Avatar.project(name, 'sm')}${name}</a></td>
        <td class="${ui.td} text-right whitespace-nowrap tabular-nums text-on-surface-variant max-sm:hidden">${p ? `${p.conversations} conversations` : ''}</td>
        <td class="${ui.td} w-0 text-right"><button class="${ui.btn('text', 'sm')}" data-act="rm-proj" data-p="${name}">Remove</button></td></tr>`;
    });
    return this.card('Projects', rows.length
      ? html`<div class="${ui.tableWrap}"><table class="w-full border-collapse text-sm"><tbody>${rows}</tbody></table></div>`
      : html`<p class="text-sm text-on-surface-variant">In no project: it can reach nothing.</p>`,
      html`<button class="${ui.btn('tonal', 'sm')}" data-act="manage-projects">${Icon.of('plus')}Add or remove</button>`);
  }

  private accessCard(m: Member): Html {
    if (m.kind === 'person') {
      if (!m.joined) return Html.empty;
      return this.card('Sign-ins', html`<div class="signins">${this.signinRows()}</div>`,
        html`<button class="${ui.btn('tonal', 'sm')}" data-act="relink" title="Signs in the device that opens it">New sign-in link</button>`);
    }
    return this.card('Token', html`<dl class="${ui.kv}"><dt>Last used</dt><dd>${m.joined ? time.ago(m.last_used) : 'never'}</dd><dt>From</dt><dd class="font-mono text-xs">${m.last_from || '-'}</dd></dl>`,
      m.kind === 'agent'
        ? html`<button class="${ui.btn('tonal', 'sm')}" data-act="relink" title="Run on the machine, it replaces the token">New install link</button>`
        : html`<button class="${ui.btn('tonal', 'sm')}" data-act="regen" title="The current token stops working at once">Regenerate token</button>`);
  }

  private removeCard(m: Member): Html {
    return this.card('Remove', this.confirming
      ? html`<div class="${ui.confirm}"><p><b>Remove ${m.name} from cafard?</b></p><ul>
          <li>${m.kind === 'person' ? 'Every sign-in ends now.' : 'Its token stops working on the next request.'}</li>
          <li>It leaves its ${m.projects.length} project${m.projects.length === 1 ? '' : 's'}.</li>
          <li>Its messages stay, shown as “${m.name} (removed)”, and the name stays taken.</li></ul>
          <div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('danger', 'sm')}" data-act="delete">Remove ${m.name}</button><button class="${ui.btn('tonal', 'sm')}" data-act="no-delete">Cancel</button></div></div>`
      : html`<p class="text-sm text-on-surface-variant">Its messages stay; it leaves its projects and its access ends.</p>`,
      this.confirming ? undefined : html`<button class="${ui.btn('danger-text', 'sm')}" data-act="ask-delete">Remove ${m.name}…</button>`);
  }

  private signinRows(): Html {
    if (!this.signins) return html`<p class="text-sm text-on-surface-variant">Loading…</p>`;
    if (!this.signins.length) return html`<p class="text-sm text-on-surface-variant">Not signed in anywhere.</p>`;
    return html`<div class="${ui.tableWrap}"><table class="w-full border-collapse text-sm"><tbody>${this.signins.map((s) => html`<tr class="${ui.tr}"><td class="${ui.td} font-semibold">${s.what}</td><td class="${ui.td} font-mono text-xs text-on-surface-variant">${s.from}</td><td class="${ui.td} text-on-surface-variant">${time.ago(s.last_used)}</td><td class="${ui.td} w-0 text-right"><button class="${ui.btn('text', 'sm')}" data-act="signout-one" data-i="${String(s.id)}">Sign out</button></td></tr>`)}</tbody></table></div>`;
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
      'open-member': (t) => void this.app.go(pathOf('members', t.dataset['v'] ?? '')),
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
    const head = this.itemHead(Avatar.project(p.name), p.name, `${p.retention_days}-day retention · ${Bytes.format(p.quota_bytes)} quota`,
      html`${p.archived ? html`<span class="${ui.pill.muted}">archived · read-only</span>` : html`<span class="${ui.pill.ok}">open</span>`}<button class="${ui.btn('outlined', 'sm')}" data-act="archive">${p.archived ? 'Unarchive' : 'Archive'}</button>`);
    const count = (k: string) => p.members.filter((m) => m.kind === k).length;
    const used = p.quota_bytes ? Math.round((p.bytes / p.quota_bytes) * 100) : 0;
    const stats = this.stats([
      { label: 'Conversations', value: p.conversations },
      { label: 'Files', value: p.files, hint: Bytes.format(p.bytes) },
      { label: 'Quota used', value: `${used}%`, hint: `of ${Bytes.format(p.quota_bytes)}` },
      { label: 'Members', value: p.members.length, hint: `${count('person')} people · ${count('agent')} agents · ${count('device')} devices` },
    ]);
    return html`${head}${stats}${this.membersCard(p)}${this.filesCard(p)}${this.deleteCard(p)}`;
  }

  private membersCard(p: Project): Html {
    const kind = (k: string) => (k === 'person' ? 'Person' : k === 'agent' ? 'Agent · reads and writes' : 'Device · sends only');
    const rows = p.members.map((m) => html`<tr class="${ui.trLink}" role="button" tabindex="0" data-act="open-member" data-v="${m.name}">
      <td class="${ui.td}"><span class="flex items-center gap-2">${Avatar.of(m, 'sm')}<span class="font-semibold">${m.display_name || m.name}</span>${m.display_name && m.display_name !== m.name ? html`<span class="text-on-surface-variant">@${m.name}</span>` : ''}</span></td>
      <td class="${ui.td} text-on-surface-variant">${kind(m.kind)}</td><td class="${ui.td} w-0 text-on-surface-variant">${Icon.of('chev')}</td></tr>`);
    return this.card('Members', rows.length
      ? html`<div class="${ui.tableWrap}"><table class="w-full border-collapse text-sm"><tbody>${rows}</tbody></table></div>`
      : html`<p class="text-sm text-on-surface-variant">Nobody yet.</p>`,
      p.archived ? undefined : html`<button class="${ui.btn('tonal', 'sm')}" data-act="manage-members">${Icon.of('plus')}Add or remove</button>`);
  }

  private filesCard(p: Project): Html {
    const option = (v: number, label: string, current: number) => html`<option value="${String(v)}" ${Html.raw(v === current ? 'selected' : '')}>${label}</option>`;
    const days = [7, 14, 30, 60, 90, 365];
    if (!days.includes(p.retention_days)) days.push(p.retention_days);
    const quotas = [1, 2, 5, 10, 20, 50].map((g) => g * 2 ** 30);
    if (!quotas.includes(p.quota_bytes)) quotas.push(p.quota_bytes);
    return this.card('Files', html`${Meter.of(p)}
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="${ui.field}"><label class="${ui.label}" for="pRet">Retention</label><select class="${ui.input}" id="pRet">${days.sort((a, b) => a - b).map((d) => option(d, `${d} days`, p.retention_days))}</select></div>
        <div class="${ui.field}"><label class="${ui.label}" for="pQuota">Quota</label><select class="${ui.input}" id="pQuota">${quotas.sort((a, b) => a - b).map((b) => option(b, Bytes.format(b), p.quota_bytes))}</select></div></div>`);
  }

  private deleteCard(p: Project): Html {
    return this.card('Delete', this.confirming
      ? html`<div class="${ui.confirm}"><p><b>This cannot be undone.</b> Deleting ${p.name} removes, for every member:</p><ul><li>${p.conversations} conversations and their messages</li><li>${p.files} files, ${Bytes.format(p.bytes)}</li><li>${p.members.length} memberships</li></ul>
          <div class="${ui.field}"><label class="${ui.label}" for="pName">Type <b>${p.name}</b> to confirm</label><input class="${ui.input}" id="pName" autocomplete="off"></div>
          <div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('danger', 'sm')}" id="pDel" data-act="p-delete" disabled>Delete ${p.name}</button><button class="${ui.btn('tonal', 'sm')}" data-act="no-delete">Cancel</button></div></div>`
      : html`<p class="text-sm text-on-surface-variant">Removes its conversations, messages and files for every member.</p>`,
      this.confirming ? undefined : html`<button class="${ui.btn('danger-text', 'sm')}" data-act="ask-delete">Delete ${p.name}…</button>`);
  }
}
