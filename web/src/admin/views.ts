import { Component, mayAutofocus } from '@shared/component';
import { Bytes, time } from '@shared/format';
import { Html, escape, html } from '@shared/html';
import { LANGS, i18n, n, t } from '@shared/i18n';
import { Icon } from '@shared/icons';
import { ui } from '@shared/ui';
import { Avatar, KindName, Meter, Switch } from '@shared/widgets';
import type { AdminApp } from './app';
import { CreateMemberFlow, CreateProjectFlow } from './create';
import { pathOf } from './route';
import { AdminStore } from './store';
import type { Config, Member } from './types';
import { View } from './view';

function linkPill(m: Member): Html {
  if (m.joined || !m.link) return Html.empty;
  switch (m.link.state) {
    case 'waiting': return html`<span class="${ui.pill.warn}">${t('link waiting')}</span>`;
    case 'expired': return html`<span class="${ui.pill.err}">${t('link expired')}</span>`;
    case 'cancelled': return html`<span class="${ui.pill.muted}">${t('link cancelled')}</span>`;
    default: return Html.empty;
  }
}

/** A table's head, its cells, and a name cell: the item's avatar and name. */
const TH = (label: string, extra = '') => html`<th class="${ui.th} ${extra}">${label}</th>`;
const TD = ui.td;
const WHO = 'flex min-w-0 items-center gap-2 [&_.nm]:font-semibold [&_.nm]:whitespace-nowrap';
const NUM = 'text-right tabular-nums';

/** What the members' filter calls each choice. */
function kindLabel(k: 'all' | 'person' | 'agent' | 'device'): string {
  switch (k) {
    case 'all':
      return t('All');
    case 'person':
      return t('People');
    case 'agent':
      return t('Agents');
    case 'device':
      return t('Devices');
  }
}

/** The members, as a table: one row each, a click opens its page. */
export class MembersView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      new: () => this.app.layer.open(new CreateMemberFlow(this.app), 'center'),
      filter: (el) => {
        this.app.ui.kind = el.dataset['v'] ?? 'all';
        this.render();
      },
      open: (el) => this.app.go(pathOf('members', el.dataset['v'] ?? '')),
      pause: async (el) => {
        const m = this.store.member(el.dataset['v'] ?? '');
        if (!m) return;
        await this.app.change(() => this.store.api.patch(`/members/${AdminStore.path(m.name)}`, { enabled: !m.enabled }),
          m.enabled ? t('{name} is paused.', { name: m.name }) : t('{name} works again.', { name: m.name }));
      },
    };
    this.root.addEventListener('input', (e) => {
      const el = e.target as HTMLInputElement;
      if (el.id === 'q') {
        this.app.ui.query = el.value;
        this.drawRows();
      }
    });
  }

  private rows(): Member[] {
    const q = this.app.ui.query.toLowerCase();
    const kind = this.app.ui.kind;
    return this.store.members.filter((m) => (kind === 'all' || m.kind === kind) &&
      (!q || [m.name, m.display_name, m.description, ...m.projects].join(' ').toLowerCase().includes(q)));
  }

  protected view(): Html {
    const members = this.store.members;
    const count = (k: string) => members.filter((m) => k === 'all' || m.kind === k).length;
    const seg = (['all', 'person', 'agent', 'device'] as const).map((k) =>
      html`<button class="${ui.segBtn}" aria-pressed="${String(this.app.ui.kind === k)}" data-act="filter" data-v="${k}">${kindLabel(k)} <span class="font-normal text-on-surface-variant">${count(k)}</span></button>`);
    const tools = html`<label class="${ui.search}">${Icon.of('search')}<input id="q" type="search" placeholder="${t('Search')}" value="${this.app.ui.query}" autocomplete="off"></label>
      <div class="${ui.seg}" role="group" aria-label="${t('Show')}">${seg}</div>
      <button class="${ui.btn('filled', 'sm')}" data-act="new">${Icon.of('plus')}${t('New')}</button>`;
    return html`${this.card(t('Members'), html`<div class="${ui.tableWrap}"><table class="${ui.table}"><thead><tr class="${ui.tr}">${TH(t('Name'))}${TH(t('Projects'))}${TH(t('Last used'))}${TH(t('From'))}${TH(t('Active'), 'text-right')}</tr></thead><tbody></tbody></table></div>`, tools)}`;
  }

  protected override afterRender(): void {
    super.afterRender();
    this.drawRows();
  }

  /** Draws the rows only, so the search keeps its focus while typing. */
  private drawRows(): void {
    const rows = this.rows().map((m) => {
      const tags = html`${m.kind !== 'person' ? html`<span class="${ui.kind[m.kind] ?? ''}">${KindName.of(m.kind)}</span>` : ''}${linkPill(m)}`;
      const chips = m.projects.length ? m.projects.map((p) => html`<span class="${ui.chip}">${p}</span>`) : html`<span class="${ui.dim}">${t('none')}</span>`;
      const used = m.joined || m.kind === 'device' ? time.ago(m.last_used) : t('not joined');
      return html`<tr class="${ui.trLink} ${m.enabled ? '' : 'opacity-55'}" role="button" tabindex="0" data-act="open" data-v="${m.name}">
        <td class="${TD}"><span class="${WHO}">${Avatar.of(m, 'sm')}<span class="nm">${m.display_name || m.name}</span>${m.display_name && m.display_name !== m.name ? html`<span class="${ui.dim}">@${m.name}</span>` : ''}${tags}</span>${m.description ? html`<span class="mt-0.5 ml-9 block max-w-[60ch] truncate text-[12.5px] text-on-surface-variant">${m.description}</span>` : ''}</td>
        <td class="${TD}"><span class="flex flex-wrap gap-1">${chips}</span></td><td class="${TD} whitespace-nowrap text-[13px]">${used}</td><td class="${TD} font-mono text-xs text-on-surface-variant">${m.last_from ?? ''}</td>
        <td class="${TD} text-right">${Switch.of(m.enabled, 'pause', m.name, m.enabled ? t('Pause {name}', { name: m.name }) : t('Resume {name}', { name: m.name }))}</td></tr>`;
    });
    this.$('tbody').innerHTML = (rows.length ? Html.join(rows) : html`<tr><td colspan="5" class="${ui.empty}">${t('Nobody matches.')}</td></tr>`).value;
  }
}

/** The projects, as a table. */
export class ProjectsView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      'new-project': () => this.app.layer.open(new CreateProjectFlow(this.app), 'center'),
      open: (el) => this.app.go(pathOf('projects', el.dataset['v'] ?? '')),
    };
  }

  protected view(): Html {
    const ps = [...this.store.projects].sort((a, b) => Number(!!a.archived) - Number(!!b.archived) || a.name.localeCompare(b.name));
    const rows = ps.map((p) => {
      const stack = html`${p.members.slice(0, 6).map((m) => Avatar.of(m, 'sm'))}${p.members.length > 6 ? html`<span class="ml-2 self-center text-xs text-on-surface-variant">+${p.members.length - 6}</span>` : ''}`;
      return html`<tr class="${ui.trLink} ${p.archived ? 'opacity-55' : ''}" role="button" tabindex="0" data-act="open" data-v="${p.name}">
        <td class="${TD}"><span class="${WHO}">${Avatar.project(p.name, 'sm')}<span class="nm">${p.name}</span>${p.archived ? html`<span class="${ui.pill.muted}">${t('archived')}</span>` : ''}</span></td>
        <td class="${TD}"><span class="flex pl-2 [&>span]:-ml-2 [&>span]:ring-2 [&>span]:ring-surface-lowest">${stack}</span></td><td class="${TD} ${NUM}">${p.conversations}</td><td class="${TD} ${NUM}">${p.files}</td><td class="${TD} min-w-56">${Meter.of(p)}</td></tr>`;
    });
    return this.card(t('Projects'), html`<div class="${ui.tableWrap}"><table class="${ui.table}"><thead><tr class="${ui.tr}">${TH(t('Name'))}${TH(t('Members'))}${TH(t('Conversations'), 'text-right')}${TH(t('Files'), 'text-right')}${TH(t('Storage'))}</tr></thead>
      <tbody>${rows.length ? rows : html`<tr><td colspan="5" class="${ui.empty}">${t('No project yet.')}</td></tr>`}</tbody></table></div>`,
      html`<button class="${ui.btn('filled', 'sm')}" data-act="new-project">${Icon.of('plus')}${t('New project')}</button>`);
  }
}

/** The overview, at the root: counts, what to review, links waiting, storage. */
export class OverviewView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      'cancel-link': async (el) => {
        await this.app.change(() => this.store.api.delete(`/links/${el.dataset['v']}`), t('The link no longer works.'));
      },
      open: (el) => this.app.go(pathOf('projects', el.dataset['v'] ?? '')),
    };
  }

  protected view(): Html {
    const ms = this.store.members;
    const ps = this.store.projects;
    const kinds = (k: string) => ms.filter((m) => m.kind === k).length;
    const total = ps.reduce((s, p) => s + p.bytes, 0);
    const files = ps.reduce((s, p) => s + p.files, 0);
    const waiting = this.store.links.filter((l) => l.state === 'waiting').map((l) => {
      const what = l.kind === 'agent' ? t('install') : t('sign-in');
      return html`<tr class="${ui.tr}"><td class="${TD}"><a class="${ui.link}" href="${pathOf('members', l.member)}">${l.member}</a></td><td class="${TD}">${what}</td><td class="${TD} text-[13px]">${time.until(l.expires_at)}</td><td class="${TD} text-right"><button class="${ui.btn('danger-text', 'sm')}" data-act="cancel-link" data-v="${String(l.id)}">${t('Cancel')}</button></td></tr>`;
    });
    const storage = ps.map((p) => html`<tr class="${ui.trLink}" role="button" tabindex="0" data-act="open" data-v="${p.name}"><td class="${TD}"><span class="${WHO}">${Avatar.project(p.name, 'sm')}<span class="nm">${p.name}</span>${p.archived ? html`<span class="${ui.pill.muted}">${t('archived')}</span>` : ''}</span></td><td class="${TD} ${NUM}">${p.files}</td><td class="${TD} min-w-56">${Meter.of(p)}</td></tr>`);
    const stats = this.stats([
      { label: t('People'), value: kinds('person'), hint: t('{n} not joined', { n: ms.filter((m) => m.kind === 'person' && !m.joined).length }), href: '/members' },
      { label: t('Agents'), value: kinds('agent'), hint: t('{n} active', { n: ms.filter((m) => m.kind === 'agent' && m.enabled).length }), href: '/members' },
      { label: t('Devices'), value: kinds('device'), hint: t('{n} active', { n: ms.filter((m) => m.kind === 'device' && m.enabled).length }), href: '/members' },
      { label: t('Projects'), value: ps.length, hint: t('{n} archived', { n: ps.filter((p) => p.archived).length }), href: '/projects' },
      { label: t('Files'), value: Bytes.format(total), hint: n(files, '{n} file', '{n} files') },
    ]);
    // Links waiting only while there are some: an empty card is a card to
    // read for nothing.
    const links = waiting.length
      ? this.card(t('Links waiting'), html`<div class="${ui.tableWrap}"><table class="${ui.table}"><thead><tr class="${ui.tr}">${TH(t('For'))}${TH(t('Link'))}${TH(t('Expires'))}${TH('')}</tr></thead><tbody>${waiting}</tbody></table></div>`)
      : Html.empty;
    return html`${stats}${links}
      ${this.card(t('Storage'), html`<div class="${ui.tableWrap}"><table class="${ui.table}"><thead><tr class="${ui.tr}">${TH(t('Project'))}${TH(t('Files'), 'text-right')}${TH(t('Used'))}</tr></thead><tbody>${storage.length ? storage : html`<tr><td colspan="3" class="${ui.empty}">${t('No project yet.')}</td></tr>`}</tbody></table></div>`)}`;
  }
}

/**
 * Settings: the public URL, which changes here, the rest of the
 * configuration, which changes only in the file on the box, and the
 * language this browser shows the UI in.
 */
export class SettingsView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      'save-url': () => this.saveURL(this.$<HTMLInputElement>('#pubUrl').value),
      'reset-url': () => this.saveURL(''),
      lang: (el) => {
        const lang = LANGS.find((l) => l.id === el.dataset['v']);
        if (lang && lang.id !== i18n.lang) i18n.choose(lang.id);
      },
    };
    this.root.addEventListener('submit', (e) => {
      e.preventDefault();
      void this.saveURL(this.$<HTMLInputElement>('#pubUrl').value);
    });
  }

  private async saveURL(url: string): Promise<void> {
    const c = await this.app.change(() => this.store.api.put<Config>('/settings/public-url', { url }), url ? t('Links now point at the new address.') : t("Back to the file's address."));
    if (c) {
      this.store.config = c;
      this.render();
    }
  }

  protected view(): Html {
    const c = this.store.config;
    if (!c) return html`<p class="${ui.empty}">${t('Loading…')}</p>`;
    // The file's URL is code inside the sentence: the sentence is translated
    // whole, and the code put in its {url}.
    const over = Html.raw(escape(t('Set here, over {url} from the file')).replace('{url}', () => `<code>${escape(c.public_url_file)}</code>`));
    const from = c.public_url_set
      ? html`<span class="${ui.dim} text-xs">${over}</span>`
      : html`<span class="${ui.dim} font-mono text-xs">public-url, /etc/cafard/config.yaml</span>`;
    const langs = LANGS.map((l) => html`<button class="${ui.segBtn}" type="button" lang="${l.id}" aria-pressed="${String(l.id === i18n.lang)}" data-act="lang" data-v="${l.id}">${l.name}</button>`);
    return html`${this.card(t('Public URL'), html`<form class="flex flex-wrap items-center gap-2" novalidate>
        <input class="${ui.input} min-w-0 flex-[1_1_18rem] font-mono" id="pubUrl" type="url" inputmode="url" autocomplete="off" spellcheck="false" aria-label="${t('Public URL')}" value="${c.public_url}" placeholder="https://cafard.example.com" title="${t("Where join links, upload links and agents' MCP configs point. Links already given keep theirs.")}">
        <button class="${ui.btn('tonal', 'sm')}" type="submit">${t('Save')}</button>
        ${c.public_url_set ? html`<button class="${ui.btn('text', 'sm')}" type="button" data-act="reset-url" title="${t('Use {url}, from the file', { url: c.public_url_file })}">${t("Use the file's")}</button>` : ''}</form>`, from)}
      ${this.card(t('Language'), html`<div class="${ui.seg} self-start" role="group" aria-label="${t('Language')}" title="${t('For this browser only')}">${langs}</div>`)}
      ${this.card(t('Listeners'), html`<dl class="${ui.kv}">
        <dt>listen</dt><dd><code>${c.listen}</code></dd>
        <dt>admin-listen</dt><dd><code>${c.admin_listen}</code></dd>
        <dt>state-dir</dt><dd><code>${c.state_dir}</code></dd>
        <dt>${t('Build')}</dt><dd><code>${c.version}</code></dd></dl>`, html`<span class="${ui.dim} font-mono text-xs">/etc/cafard/config.yaml</span>`)}
      ${this.card(t('Limits'), html`<dl class="${ui.kv}">
        <dt>${t('Largest upload')}</dt><dd>${Bytes.format(c.max_upload)}</dd>
        <dt>${t('Link life')}</dt><dd>${n(c.link_max_hours / 24, '{hours} h, at most {n} day', '{hours} h, at most {n} days', { hours: c.link_hours })}</dd>
        <dt>${t('Admin sign-in')}</dt><dd>${t('{n} h after last use', { n: c.admin_signin_hours })}</dd>
        <dt>${t('Chat sign-in')}</dt><dd>${n(c.chat_signin_days, '{n} day after last use', '{n} days after last use')}</dd></dl>`)}`;
  }
}

/** The sign-in form, shown instead of the shell when nobody is signed in. */
export class SignInView extends Component {
  constructor(
    private readonly app: AdminApp,
    private readonly message?: string,
  ) {
    super('div', 'grid min-h-full place-items-center px-4 py-6');
    this.root.addEventListener('submit', (e) => {
      e.preventDefault();
      void this.submit();
    });
  }

  protected view(): Html {
    return html`<form class="${ui.card} w-full max-w-sm gap-4 p-6" novalidate>
      <div class="flex items-center gap-2 text-lg font-bold [&_svg]:size-7 [&_svg]:text-primary">${Icon.logo} cafard <small class="text-[13px] font-semibold text-on-surface-variant">admin</small></div>
      ${this.message ? html`<p class="${ui.error}" role="alert">${this.message}</p>` : ''}
      <input type="text" name="username" value="admin" autocomplete="username" hidden>
      <div class="${ui.field}"><label class="${ui.label}" for="p">${t('Admin password')}</label><input class="${ui.input}" id="p" type="password" autocomplete="current-password" required></div>
      <span class="${ui.error}" id="sErr" hidden></span>
      <button class="${ui.btn('filled')}" type="submit">${t('Sign in')}</button></form>`;
  }

  focus(): void {
    if (mayAutofocus()) this.find<HTMLInputElement>('#p')?.focus();
  }

  private async submit(): Promise<void> {
    const err = this.$('#sErr');
    try {
      await this.app.store.signIn(this.$<HTMLInputElement>('#p').value);
      await this.app.showShell();
    } catch (e) {
      err.hidden = false;
      err.textContent = e instanceof Error ? e.message : String(e);
    }
  }
}
