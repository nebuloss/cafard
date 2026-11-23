import { Component } from '@shared/component';
import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { ui } from '@shared/ui';
import { Avatar, Meter, Switch } from '@shared/widgets';
import type { AdminApp } from './app';
import { CreateMemberFlow, CreateProjectFlow } from './create';
import { pathOf } from './route';
import { AdminStore } from './store';
import type { Member } from './types';
import { View } from './view';

function linkPill(m: Member): Html {
  if (m.joined || !m.link) return Html.empty;
  switch (m.link.state) {
    case 'waiting': return html`<span class="${ui.pill.warn}">link waiting</span>`;
    case 'expired': return html`<span class="${ui.pill.err}">link expired</span>`;
    case 'cancelled': return html`<span class="${ui.pill.muted}">link cancelled</span>`;
    default: return Html.empty;
  }
}

/** A table's head, its cells, and a name cell: the item's avatar and name. */
const TH = (label: string, extra = '') => html`<th class="${ui.th} ${extra}">${label}</th>`;
const TD = ui.td;
const WHO = 'flex min-w-0 items-center gap-2 [&_.nm]:font-semibold [&_.nm]:whitespace-nowrap';
const NUM = 'text-right tabular-nums';

const KIND_LABEL: Record<string, string> = { all: 'All', person: 'People', agent: 'Agents', device: 'Devices' };

/** The members, as a table: one row each, a click opens its page. */
export class MembersView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      new: () => this.app.layer.open(new CreateMemberFlow(this.app), 'center'),
      filter: (t) => {
        this.app.ui.kind = t.dataset['v'] ?? 'all';
        this.render();
      },
      open: (t) => this.app.go(pathOf('members', t.dataset['v'] ?? '')),
      pause: async (t) => {
        const m = this.store.member(t.dataset['v'] ?? '');
        if (!m) return;
        await this.app.change(() => this.store.api.patch(`/members/${AdminStore.path(m.name)}`, { enabled: !m.enabled }),
          m.enabled ? `${m.name} is paused.` : `${m.name} works again.`);
      },
    };
    this.root.addEventListener('input', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.id === 'q') {
        this.app.ui.query = t.value;
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
      html`<button class="${ui.segBtn}" aria-pressed="${String(this.app.ui.kind === k)}" data-act="filter" data-v="${k}">${KIND_LABEL[k]} <span class="font-normal text-on-surface-variant">${count(k)}</span></button>`);
    const tools = html`<label class="${ui.search}">${Icon.of('search')}<input id="q" type="search" placeholder="Search" value="${this.app.ui.query}" autocomplete="off"></label>
      <div class="${ui.seg}" role="group" aria-label="Show">${seg}</div>
      <button class="${ui.btn('filled', 'sm')}" data-act="new">${Icon.of('plus')}New</button>`;
    return html`${this.card('Members', html`<div class="${ui.tableWrap}"><table class="${ui.table}"><thead><tr class="${ui.tr}">${TH('Name')}${TH('Projects')}${TH('Last used')}${TH('From')}${TH('Active', 'text-right')}</tr></thead><tbody></tbody></table></div>`, tools)}`;
  }

  protected override afterRender(): void {
    super.afterRender();
    this.drawRows();
  }

  /** Draws the rows only, so the search keeps its focus while typing. */
  private drawRows(): void {
    const rows = this.rows().map((m) => {
      const tags = html`${m.kind !== 'person' ? html`<span class="${ui.kind[m.kind] ?? ''}">${m.kind}</span>` : ''}${linkPill(m)}`;
      const chips = m.projects.length ? m.projects.map((p) => html`<span class="${ui.chip}">${p}</span>`) : html`<span class="${ui.dim}">none</span>`;
      const used = m.joined || m.kind === 'device' ? time.ago(m.last_used) : 'not joined';
      return html`<tr class="${ui.trLink} ${m.enabled ? '' : 'opacity-55'}" role="button" tabindex="0" data-act="open" data-v="${m.name}">
        <td class="${TD}"><span class="${WHO}">${Avatar.of(m, 'sm')}<span class="nm">${m.display_name || m.name}</span>${m.display_name && m.display_name !== m.name ? html`<span class="${ui.dim}">@${m.name}</span>` : ''}${tags}</span>${m.description ? html`<span class="mt-0.5 ml-9 block max-w-[60ch] truncate text-[12.5px] text-on-surface-variant">${m.description}</span>` : ''}</td>
        <td class="${TD}"><span class="flex flex-wrap gap-1">${chips}</span></td><td class="${TD} whitespace-nowrap text-[13px]">${used}</td><td class="${TD} font-mono text-xs text-on-surface-variant">${m.last_from ?? ''}</td>
        <td class="${TD} text-right">${Switch.of(m.enabled, 'pause', m.name, `${m.enabled ? 'Pause' : 'Resume'} ${m.name}`)}</td></tr>`;
    });
    this.$('tbody').innerHTML = (rows.length ? Html.join(rows) : html`<tr><td colspan="5" class="${ui.empty}">Nobody matches.</td></tr>`).value;
  }
}

/** The projects, as a table. */
export class ProjectsView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      'new-project': () => this.app.layer.open(new CreateProjectFlow(this.app), 'center'),
      open: (t) => this.app.go(pathOf('projects', t.dataset['v'] ?? '')),
    };
  }

  protected view(): Html {
    const ps = [...this.store.projects].sort((a, b) => Number(!!a.archived) - Number(!!b.archived) || a.name.localeCompare(b.name));
    const rows = ps.map((p) => {
      const stack = html`${p.members.slice(0, 6).map((m) => Avatar.of(m, 'sm'))}${p.members.length > 6 ? html`<span class="ml-2 self-center text-xs text-on-surface-variant">+${p.members.length - 6}</span>` : ''}`;
      return html`<tr class="${ui.trLink} ${p.archived ? 'opacity-55' : ''}" role="button" tabindex="0" data-act="open" data-v="${p.name}">
        <td class="${TD}"><span class="${WHO}">${Avatar.project(p.name, 'sm')}<span class="nm">${p.name}</span>${p.archived ? html`<span class="${ui.pill.muted}">archived</span>` : ''}</span></td>
        <td class="${TD}"><span class="flex pl-2 [&>span]:-ml-2 [&>span]:ring-2 [&>span]:ring-surface-lowest">${stack}</span></td><td class="${TD} ${NUM}">${p.conversations}</td><td class="${TD} ${NUM}">${p.files}</td><td class="${TD} min-w-56">${Meter.of(p)}</td></tr>`;
    });
    return this.card('Projects', html`<div class="${ui.tableWrap}"><table class="${ui.table}"><thead><tr class="${ui.tr}">${TH('Name')}${TH('Members')}${TH('Conversations', 'text-right')}${TH('Files', 'text-right')}${TH('Storage')}</tr></thead>
      <tbody>${rows.length ? rows : html`<tr><td colspan="5" class="${ui.empty}">No project yet.</td></tr>`}</tbody></table></div>`,
      html`<button class="${ui.btn('filled', 'sm')}" data-act="new-project">${Icon.of('plus')}New project</button>`);
  }
}

/** The overview, at the root: counts, what to review, links waiting, storage. */
export class OverviewView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      'cancel-link': async (t) => {
        await this.app.change(() => this.store.api.delete(`/links/${t.dataset['v']}`), 'The link no longer works.');
      },
      open: (t) => this.app.go(pathOf('projects', t.dataset['v'] ?? '')),
    };
  }

  protected view(): Html {
    const ms = this.store.members;
    const ps = this.store.projects;
    const n = (k: string) => ms.filter((m) => m.kind === k).length;
    const total = ps.reduce((s, p) => s + p.bytes, 0);
    const files = ps.reduce((s, p) => s + p.files, 0);
    const waiting = this.store.links.filter((l) => l.state === 'waiting').map((l) => {
      const what = l.kind === 'agent' ? 'install' : 'sign-in';
      return html`<tr class="${ui.tr}"><td class="${TD}"><a class="${ui.link}" href="${pathOf('members', l.member)}">${l.member}</a></td><td class="${TD}">${what}</td><td class="${TD} text-[13px]">${time.until(l.expires_at)}</td><td class="${TD} text-right"><button class="${ui.btn('danger-text', 'sm')}" data-act="cancel-link" data-v="${String(l.id)}">Cancel</button></td></tr>`;
    });
    const storage = ps.map((p) => html`<tr class="${ui.trLink}" role="button" tabindex="0" data-act="open" data-v="${p.name}"><td class="${TD}"><span class="${WHO}">${Avatar.project(p.name, 'sm')}<span class="nm">${p.name}</span>${p.archived ? html`<span class="${ui.pill.muted}">archived</span>` : ''}</span></td><td class="${TD} ${NUM}">${p.files}</td><td class="${TD} min-w-56">${Meter.of(p)}</td></tr>`);
    const stats = this.stats([
      { label: 'People', value: n('person'), hint: `${ms.filter((m) => m.kind === 'person' && !m.joined).length} not joined`, href: '/members' },
      { label: 'Agents', value: n('agent'), hint: `${ms.filter((m) => m.kind === 'agent' && m.enabled).length} active`, href: '/members' },
      { label: 'Devices', value: n('device'), hint: `${ms.filter((m) => m.kind === 'device' && m.enabled).length} active`, href: '/members' },
      { label: 'Projects', value: ps.length, hint: `${ps.filter((p) => p.archived).length} archived`, href: '/projects' },
      { label: 'Files', value: Bytes.format(total), hint: `${files} files` },
    ]);
    // Links waiting only while there are some: an empty card is a card to
    // read for nothing.
    const links = waiting.length
      ? this.card('Links waiting', html`<div class="${ui.tableWrap}"><table class="${ui.table}"><thead><tr class="${ui.tr}">${TH('For')}${TH('Link')}${TH('Expires')}${TH('')}</tr></thead><tbody>${waiting}</tbody></table></div>`)
      : Html.empty;
    return html`${stats}${links}
      ${this.card('Storage', html`<div class="${ui.tableWrap}"><table class="${ui.table}"><thead><tr class="${ui.tr}">${TH('Project')}${TH('Files', 'text-right')}${TH('Used')}</tr></thead><tbody>${storage.length ? storage : html`<tr><td colspan="3" class="${ui.empty}">No project yet.</td></tr>`}</tbody></table></div>`)}`;
  }
}

/** Settings: the configuration, which changes only in the file on the box. */
export class SettingsView extends View {
  protected view(): Html {
    const c = this.store.config;
    if (!c) return html`<p class="${ui.empty}">Loading…</p>`;
    return html`${this.card('Listeners', html`<dl class="${ui.kv}">
        <dt>listen</dt><dd><code>${c.listen}</code></dd>
        <dt>public-url</dt><dd><code>${c.public_url}</code></dd>
        <dt>admin-listen</dt><dd><code>${c.admin_listen}</code></dd>
        <dt>state-dir</dt><dd><code>${c.state_dir}</code></dd>
        <dt>Build</dt><dd><code>${c.version}</code></dd></dl>`, html`<span class="${ui.dim} font-mono text-xs">/etc/cafard/config.yaml</span>`)}
      ${this.card('Limits', html`<dl class="${ui.kv}">
        <dt>Largest upload</dt><dd>${Bytes.format(c.max_upload)}</dd>
        <dt>Link life</dt><dd>${c.link_hours} h, at most ${c.link_max_hours / 24} days</dd>
        <dt>Admin sign-in</dt><dd>${c.admin_signin_hours} h after last use</dd>
        <dt>Chat sign-in</dt><dd>${c.chat_signin_days} days after last use</dd></dl>`)}`;
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
      <div class="${ui.field}"><label class="${ui.label}" for="p">Admin password</label><input class="${ui.input}" id="p" type="password" autocomplete="current-password" required></div>
      <span class="${ui.error}" id="sErr" hidden></span>
      <button class="${ui.btn('filled')}" type="submit">Sign in</button></form>`;
  }

  focus(): void {
    this.find<HTMLInputElement>('#p')?.focus();
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
