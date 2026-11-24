import { Bytes, time } from '@shared/format';
import { Html, escape, html } from '@shared/html';
import { n, t } from '@shared/i18n';
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

  /** What a page says when its item is not there: why, in text, and the way back. */
  protected gone(text: string, back: string): Html {
    return html`<div class="${ui.empty}"><p>${text}</p><p><a href="${back}">${t('Back to the list')}</a></p></div>`;
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
        if (m) await this.app.change(() => this.store.api.patch(path(), { enabled: !m.enabled }), m.enabled ? t('{name} is paused.', { name: m.name }) : t('{name} works again.', { name: m.name }));
      },
      'save-desc': async () => {
        await this.app.change(() => this.store.api.patch(path(), { description: this.$<HTMLTextAreaElement>('#desc').value }), t('Saved.'));
      },
      'rm-proj': async (el) => {
        const m = this.member();
        const p = el.dataset['p'] ?? '';
        if (m) await this.app.change(() => this.store.api.patch(path(), { projects: m.projects.filter((x) => x !== p) }), t('Removed from {project}.', { project: p }));
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
      'signout-one': async (el) => {
        await this.app.attempt(() => this.store.api.delete(`${path()}/signins/${el.dataset['i']}`), t('Signed out.'));
        this.signins = null;
        this.render();
      },
      delete: async () => {
        const ok = await this.app.attempt(() => this.store.api.delete(path()), t('{name} is removed. Their messages stay.', { name: this.name }));
        if (ok !== null) await this.app.go('/members');
      },
    };
  }

  private member(): Member | undefined {
    return this.store.member(this.name);
  }

  protected view(): Html {
    const m = this.member();
    if (!m) return this.gone(t('There is no member called {name}: it was removed, or the link is wrong.', { name: this.name }), '/members');
    const kindTxt = m.kind === 'person' ? t('Person') : m.kind === 'agent' ? t('Agent') : t('Device · sends only');
    const state = !m.enabled
      ? html`<span class="${ui.pill.muted}">${t('paused')}</span>`
      : !m.joined ? html`<span class="${ui.pill.warn}">${t('not joined')}</span>` : html`<span class="${ui.pill.ok}">${t('active')}</span>`;
    const head = this.itemHead(Avatar.of(m), m.display_name || m.name, `${kindTxt} · @${m.name}`,
      html`${state}${Switch.of(m.enabled, 'pause', m.name, m.enabled ? t('Pause {name}', { name: m.name }) : t('Resume {name}', { name: m.name }))}`);
    const used = m.joined ? time.ago(m.last_used) : t('never');
    const stats = this.stats([
      { label: t('Projects'), value: m.projects.length, hint: m.projects.length ? m.projects.slice(0, 3).join(', ') + (m.projects.length > 3 ? '…' : '') : t('reaches nothing') },
      { label: m.kind === 'person' ? t('Last sign-in') : t('Last used'), value: used, hint: m.last_from ?? '' },
      m.kind === 'person'
        ? { label: t('Sign-ins'), value: m.signins ?? 0, hint: t('chat app') }
        : { label: t('Token'), value: m.joined ? t('in use') : t('not yet'), hint: m.kind === 'agent' ? t('from its install link') : t('from its setup link') },
    ]);
    return html`${head}${stats}${this.linkCard(m)}${this.descriptionCard(m)}${this.projectsCard(m)}${this.accessCard(m)}${this.removeCard(m)}`;
  }

  /** A member that has not joined: its link, and a new one. */
  private linkCard(m: Member): Html {
    if (m.joined) return Html.empty;
    const l = m.link;
    const until = l ? time.until(l.expires_at) : '';
    const waiting = m.kind === 'agent' ? t('The install link is waiting · {until}.', { until })
      : m.kind === 'device' ? t('The setup link is waiting · {until}.', { until })
      : t('The sign-in link is waiting · {until}.', { until });
    const msg = !l ? t('No link yet.')
      : l.state === 'waiting' ? waiting
      : l.state === 'expired' ? t('The link expired before it was used.')
      : t('The link was cancelled.');
    const title = m.kind === 'agent' ? t('Install link') : m.kind === 'device' ? t('Setup link') : t('Sign-in link');
    const button = m.kind === 'agent' ? t('New install link') : m.kind === 'device' ? t('New setup link') : t('New sign-in link');
    return this.card(title, html`<p class="text-sm text-on-surface-variant">${msg}</p>`,
      html`<button class="${ui.btn('filled', 'sm')}" data-act="relink">${button}</button>`);
  }

  private descriptionCard(m: Member): Html {
    if (m.kind === 'person') return Html.empty;
    return this.card(m.kind === 'agent' ? t('What it can do') : t('Note'),
      html`<textarea class="${ui.textarea}" id="desc" aria-label="${t('Description')}" placeholder="${m.kind === 'agent' ? t('Serial console of the be98 unit; can flash it') : ''}">${m.description ?? ''}</textarea>`,
      html`<button class="${ui.btn('tonal', 'sm')}" data-act="save-desc">${t('Save')}</button>`);
  }

  private projectsCard(m: Member): Html {
    const rows = m.projects.map((name) => {
      const p = this.store.project(name);
      return html`<tr class="${ui.tr}"><td class="${ui.td}"><a class="flex items-center gap-2 font-semibold text-inherit no-underline hover:underline" href="${pathOf('projects', name)}">${Avatar.project(name, 'sm')}${name}</a></td>
        <td class="${ui.td} text-right whitespace-nowrap tabular-nums text-on-surface-variant max-sm:hidden">${p ? n(p.conversations, '{n} conversation', '{n} conversations') : ''}</td>
        <td class="${ui.td} w-0 text-right"><button class="${ui.btn('text', 'sm')}" data-act="rm-proj" data-p="${name}">${t('Remove')}</button></td></tr>`;
    });
    return this.card(t('Projects'), rows.length
      ? html`<div class="${ui.tableWrap}"><table class="w-full border-collapse text-sm"><tbody>${rows}</tbody></table></div>`
      : html`<p class="text-sm text-on-surface-variant">${t('In no project: it can reach nothing.')}</p>`,
      html`<button class="${ui.btn('tonal', 'sm')}" data-act="manage-projects">${Icon.of('plus')}${t('Add or remove')}</button>`);
  }

  private accessCard(m: Member): Html {
    if (m.kind === 'person') {
      if (!m.joined) return Html.empty;
      return this.card(t('Sign-ins'), html`<div class="signins">${this.signinRows()}</div>`,
        html`<button class="${ui.btn('tonal', 'sm')}" data-act="relink" title="${t('Signs in the device that opens it')}">${t('New sign-in link')}</button>`);
    }
    return this.card(t('Token'), html`<dl class="${ui.kv}"><dt>${t('Last used')}</dt><dd>${m.joined ? time.ago(m.last_used) : t('never')}</dd><dt>${t('From')}</dt><dd class="font-mono text-xs">${m.last_from || '-'}</dd></dl>`,
      m.kind === 'agent'
        ? html`<button class="${ui.btn('tonal', 'sm')}" data-act="relink" title="${t('Run on the machine, it replaces the token')}">${t('New install link')}</button>`
        : html`<button class="${ui.btn('tonal', 'sm')}" data-act="relink" title="${t('Run on the machine: it installs mouchard and replaces the token')}">${t('New setup link')}</button>
          <button class="${ui.btn('tonal', 'sm')}" data-act="regen" title="${t('For a board with curl alone; the current token stops working at once')}">${t('Regenerate token')}</button>`);
  }

  private removeCard(m: Member): Html {
    const name = m.name;
    return this.card(t('Remove'), this.confirming
      ? html`<div class="${ui.confirm}"><p><b>${t('Remove {name} from cafard?', { name })}</b></p><ul>
          <li>${m.kind === 'person' ? t('Every sign-in ends now.') : t('Its token stops working on the next request.')}</li>
          <li>${n(m.projects.length, 'It leaves its {n} project.', 'It leaves its {n} projects.')}</li>
          <li>${t('Its messages stay, shown as “{name} (removed)”, and the name stays taken.', { name })}</li></ul>
          <div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('danger', 'sm')}" data-act="delete">${t('Remove {name}', { name })}</button><button class="${ui.btn('tonal', 'sm')}" data-act="no-delete">${t('Cancel')}</button></div></div>`
      : html`<p class="text-sm text-on-surface-variant">${t('Its messages stay; it leaves its projects and its access ends.')}</p>`,
      this.confirming ? undefined : html`<button class="${ui.btn('danger-text', 'sm')}" data-act="ask-delete">${t('Remove {name}…', { name })}</button>`);
  }

  private signinRows(): Html {
    if (!this.signins) return html`<p class="text-sm text-on-surface-variant">${t('Loading…')}</p>`;
    if (!this.signins.length) return html`<p class="text-sm text-on-surface-variant">${t('Not signed in anywhere.')}</p>`;
    return html`<div class="${ui.tableWrap}"><table class="w-full border-collapse text-sm"><tbody>${this.signins.map((s) => html`<tr class="${ui.tr}"><td class="${ui.td} font-semibold">${s.what}</td><td class="${ui.td} font-mono text-xs text-on-surface-variant">${s.from}</td><td class="${ui.td} text-on-surface-variant">${time.ago(s.last_used)}</td><td class="${ui.td} w-0 text-right"><button class="${ui.btn('text', 'sm')}" data-act="signout-one" data-i="${String(s.id)}">${t('Sign out')}</button></td></tr>`)}</tbody></table></div>`;
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
      'open-member': (el) => void this.app.go(pathOf('members', el.dataset['v'] ?? '')),
      'manage-members': () => {
        const p = this.project();
        if (p) this.app.layer.open(PickerDialog.forProject(this.app, p), 'center');
      },
      archive: async () => {
        const p = this.project();
        if (p) await this.app.change(() => this.store.api.patch(path(), { archived: !p.archived }), p.archived ? t('{name} is open again.', { name: p.name }) : t('{name} is archived and read-only.', { name: p.name }));
      },
      'p-delete': async () => {
        const ok = await this.app.attempt(() => this.store.api.delete(path(), { confirm: this.$<HTMLInputElement>('#pName').value.trim() }), t('{name} is deleted.', { name: this.name }));
        if (ok !== null) await this.app.go('/projects');
      },
    };
    this.root.addEventListener('change', (e) => {
      const el = e.target as HTMLSelectElement;
      if (el.id === 'pRet') void this.app.change(() => this.store.api.patch(path(), { retention_days: Number(el.value) }), t('Retention changed.'));
      if (el.id === 'pQuota') void this.app.change(() => this.store.api.patch(path(), { quota_bytes: Number(el.value) }), t('Quota changed.'));
    });
    this.root.addEventListener('input', (e) => {
      const el = e.target as HTMLInputElement;
      if (el.id === 'pName') this.$<HTMLButtonElement>('#pDel').disabled = el.value.trim() !== this.name;
    });
  }

  private project(): Project | undefined {
    return this.store.project(this.name);
  }

  protected view(): Html {
    const p = this.project();
    if (!p) return this.gone(t('There is no project called {name}: it was removed, or the link is wrong.', { name: this.name }), '/projects');
    const head = this.itemHead(Avatar.project(p.name), p.name, t('{days}-day retention · {quota} quota', { days: p.retention_days, quota: Bytes.format(p.quota_bytes) }),
      html`${p.archived ? html`<span class="${ui.pill.muted}">${t('archived · read-only')}</span>` : html`<span class="${ui.pill.ok}">${t('open')}</span>`}<button class="${ui.btn('outlined', 'sm')}" data-act="archive">${p.archived ? t('Unarchive') : t('Archive')}</button>`);
    const count = (k: string) => p.members.filter((m) => m.kind === k).length;
    const used = p.quota_bytes ? Math.round((p.bytes / p.quota_bytes) * 100) : 0;
    const stats = this.stats([
      { label: t('Conversations'), value: p.conversations },
      { label: t('Files'), value: p.files, hint: Bytes.format(p.bytes) },
      { label: t('Quota used'), value: `${used}%`, hint: t('of {size}', { size: Bytes.format(p.quota_bytes) }) },
      {
        label: t('Members'),
        value: p.members.length,
        hint: [n(count('person'), '{n} person', '{n} people'), n(count('agent'), '{n} agent', '{n} agents'), n(count('device'), '{n} device', '{n} devices')].join(' · '),
      },
    ]);
    return html`${head}${stats}${this.membersCard(p)}${this.filesCard(p)}${this.deleteCard(p)}`;
  }

  private membersCard(p: Project): Html {
    const kind = (k: string) => (k === 'person' ? t('Person') : k === 'agent' ? t('Agent · reads and writes') : t('Device · sends only'));
    const rows = p.members.map((m) => html`<tr class="${ui.trLink}" role="button" tabindex="0" data-act="open-member" data-v="${m.name}">
      <td class="${ui.td}"><span class="flex items-center gap-2">${Avatar.of(m, 'sm')}<span class="font-semibold">${m.display_name || m.name}</span>${m.display_name && m.display_name !== m.name ? html`<span class="text-on-surface-variant">@${m.name}</span>` : ''}</span></td>
      <td class="${ui.td} text-on-surface-variant">${kind(m.kind)}</td><td class="${ui.td} w-0 text-on-surface-variant">${Icon.of('chev')}</td></tr>`);
    return this.card(t('Members'), rows.length
      ? html`<div class="${ui.tableWrap}"><table class="w-full border-collapse text-sm"><tbody>${rows}</tbody></table></div>`
      : html`<p class="text-sm text-on-surface-variant">${t('Nobody yet.')}</p>`,
      p.archived ? undefined : html`<button class="${ui.btn('tonal', 'sm')}" data-act="manage-members">${Icon.of('plus')}${t('Add or remove')}</button>`);
  }

  private filesCard(p: Project): Html {
    const option = (v: number, label: string, current: number) => html`<option value="${String(v)}" ${Html.raw(v === current ? 'selected' : '')}>${label}</option>`;
    const days = [7, 14, 30, 60, 90, 365];
    if (!days.includes(p.retention_days)) days.push(p.retention_days);
    const quotas = [1, 2, 5, 10, 20, 50].map((g) => g * 2 ** 30);
    if (!quotas.includes(p.quota_bytes)) quotas.push(p.quota_bytes);
    return this.card(t('Files'), html`${Meter.of(p)}
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="${ui.field}"><label class="${ui.label}" for="pRet">${t('Retention')}</label><select class="${ui.input}" id="pRet">${days.sort((a, b) => a - b).map((d) => option(d, n(d, '{n} day', '{n} days'), p.retention_days))}</select></div>
        <div class="${ui.field}"><label class="${ui.label}" for="pQuota">${t('Quota')}</label><select class="${ui.input}" id="pQuota">${quotas.sort((a, b) => a - b).map((b) => option(b, Bytes.format(b), p.quota_bytes))}</select></div></div>`);
  }

  private deleteCard(p: Project): Html {
    const name = p.name;
    // The name to type is in bold inside the sentence: the sentence is
    // translated whole, and the bold name put in its {name}.
    const typeIt = Html.raw(escape(t('Type {name} to confirm')).replace('{name}', () => `<b>${escape(name)}</b>`));
    return this.card(t('Delete'), this.confirming
      ? html`<div class="${ui.confirm}"><p><b>${t('This cannot be undone.')}</b> ${t('Deleting {name} removes, for every member:', { name })}</p><ul><li>${n(p.conversations, '{n} conversation and its messages', '{n} conversations and their messages')}</li><li>${n(p.files, '{n} file, {size}', '{n} files, {size}', { size: Bytes.format(p.bytes) })}</li><li>${n(p.members.length, '{n} membership', '{n} memberships')}</li></ul>
          <div class="${ui.field}"><label class="${ui.label}" for="pName">${typeIt}</label><input class="${ui.input}" id="pName" autocomplete="off"></div>
          <div class="flex flex-wrap items-center gap-2.5"><button class="${ui.btn('danger', 'sm')}" id="pDel" data-act="p-delete" disabled>${t('Delete {name}', { name })}</button><button class="${ui.btn('tonal', 'sm')}" data-act="no-delete">${t('Cancel')}</button></div></div>`
      : html`<p class="text-sm text-on-surface-variant">${t('Removes its conversations, messages and files for every member.')}</p>`,
      this.confirming ? undefined : html`<button class="${ui.btn('danger-text', 'sm')}" data-act="ask-delete">${t('Delete {name}…', { name })}</button>`);
  }
}
