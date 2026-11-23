import { Component } from '@shared/component';
import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { Avatar, Meter, Switch } from '@shared/widgets';
import type { AdminApp } from './app';
import { CreateMemberFlow, CreateProjectFlow } from './create';
import { pathOf } from './route';
import { AdminStore } from './store';
import type { AuditEntry, Member } from './types';
import { View } from './view';

function linkPill(m: Member): Html {
  if (m.joined || !m.link) return Html.empty;
  switch (m.link.state) {
    case 'waiting': return html`<span class="pill warn">link waiting</span>`;
    case 'expired': return html`<span class="pill err">link expired</span>`;
    case 'cancelled': return html`<span class="pill muted">link cancelled</span>`;
    default: return Html.empty;
  }
}

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
    const review = this.review();
    const seg = (['all', 'person', 'agent', 'device'] as const).map((k) =>
      html`<button aria-pressed="${String(this.app.ui.kind === k)}" data-act="filter" data-v="${k}">${KIND_LABEL[k]} <span class="c">${count(k)}</span></button>`);
    const tools = html`<label class="search">${Icon.of('search')}<input id="q" type="search" placeholder="Search" value="${this.app.ui.query}" autocomplete="off"></label>
      <div class="seg" role="group" aria-label="Show">${seg}</div>
      <button class="btn filled sm" data-act="new">${Icon.of('plus')}New</button>`;
    return html`${review.length ? html`<div class="notice">${Icon.of('warn')}<p>${review.join(' · ')}</p></div>` : ''}
      ${this.card('Members', html`<div class="tbl-wrap"><table class="rows"><thead><tr><th>Name</th><th>Projects</th><th>Last used</th><th>From</th><th class="r">Active</th></tr></thead><tbody></tbody></table></div>`, tools)}`;
  }

  protected override afterRender(): void {
    super.afterRender();
    this.drawRows();
  }

  /** Draws the rows only, so the search keeps its focus while typing. */
  private drawRows(): void {
    const rows = this.rows().map((m) => {
      const tags = html`${m.kind !== 'person' ? html`<span class="kind ${m.kind}">${m.kind}</span>` : ''}${linkPill(m)}`;
      const chips = m.projects.length ? m.projects.map((p) => html`<span class="chip">${p}</span>`) : html`<span class="dim">none</span>`;
      const used = m.joined || m.kind === 'device' ? time.ago(m.last_used) : 'not joined';
      return html`<tr class="${m.enabled ? '' : 'off'}" role="button" tabindex="0" data-act="open" data-v="${m.name}">
        <td><span class="who">${Avatar.of(m, 'sm')}<span class="nm">${m.display_name || m.name}</span>${m.display_name && m.display_name !== m.name ? html`<span class="dim">@${m.name}</span>` : ''}${tags}</span>${m.description ? html`<span class="desc">${m.description}</span>` : ''}</td>
        <td><span class="chips">${chips}</span></td><td class="time">${used}</td><td class="mono dim">${m.last_from ?? ''}</td>
        <td class="r">${Switch.of(m.enabled, 'pause', m.name, `${m.enabled ? 'Pause' : 'Resume'} ${m.name}`)}</td></tr>`;
    });
    this.$('tbody').innerHTML = (rows.length ? Html.join(rows) : html`<tr><td colspan="5" class="empty">Nobody matches.</td></tr>`).value;
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
      const stack = html`${p.members.slice(0, 6).map((m) => Avatar.of(m, 'sm'))}${p.members.length > 6 ? html`<span class="more">+${p.members.length - 6}</span>` : ''}`;
      return html`<tr class="${p.archived ? 'off' : ''}" role="button" tabindex="0" data-act="open" data-v="${p.name}">
        <td><span class="who">${Avatar.project(p.name, 'sm')}<span class="nm">${p.name}</span>${p.archived ? html`<span class="pill muted">archived</span>` : ''}</span></td>
        <td><span class="stack">${stack}</span></td><td class="num">${p.conversations}</td><td class="num">${p.files}</td><td class="meter-cell">${Meter.of(p)}</td></tr>`;
    });
    return this.card('Projects', html`<div class="tbl-wrap"><table class="rows"><thead><tr><th>Name</th><th>Members</th><th class="num">Conversations</th><th class="num">Files</th><th>Storage</th></tr></thead>
      <tbody>${rows.length ? rows : html`<tr><td colspan="5" class="empty">No project yet.</td></tr>`}</tbody></table></div>`,
      html`<button class="btn filled sm" data-act="new-project">${Icon.of('plus')}New project</button>`);
  }
}

export class AuditView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      afilter: (t) => {
        this.app.ui.audit = t.dataset['v'] ?? 'all';
        this.render();
      },
    };
  }

  static kindOf(e: AuditEntry): string {
    if (/signed (in|out)|sign-in/.test(e.action)) return 'signins';
    if (/project/.test(e.action)) return 'projects';
    return 'access';
  }

  protected view(): Html {
    const f = this.app.ui.audit;
    const seg = ([['all', 'All'], ['access', 'Access'], ['projects', 'Projects'], ['signins', 'Sign-ins']] as const).map(([k, l]) =>
      html`<button aria-pressed="${String(f === k)}" data-act="afilter" data-v="${k}">${l}</button>`);
    const rows = this.store.audit.filter((e) => f === 'all' || AuditView.kindOf(e) === f).map((e) =>
      html`<tr><td class="time">${time.when(e.at)}</td><td><b>${e.actor}</b> ${e.action} <b>${e.target}</b></td><td class="mono dim">${e.origin}</td></tr>`);
    return this.card('Audit log', html`<div class="tbl-wrap"><table><thead><tr><th>When</th><th>What</th><th>From</th></tr></thead><tbody>${rows.length ? rows : html`<tr><td colspan="3" class="empty">Nothing yet.</td></tr>`}</tbody></table></div>`,
      html`<div class="seg" role="group" aria-label="Show">${seg}</div>`);
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
      open: (t) => this.app.go(pathOf('projects', t.dataset['v'] ?? '', 'files')),
    };
  }

  protected view(): Html {
    const ms = this.store.members;
    const ps = this.store.projects;
    const n = (k: string) => ms.filter((m) => m.kind === k).length;
    const total = ps.reduce((s, p) => s + p.bytes, 0);
    const files = ps.reduce((s, p) => s + p.files, 0);
    const tile = (href: string, label: string, value: string | number, sub: string) =>
      html`<a class="tile" href="${href}"><span class="tl">${label}</span><span class="big">${value}</span><span class="dim">${sub}</span></a>`;
    const review = this.review();
    const waiting = this.store.links.filter((l) => l.state === 'waiting').map((l) => {
      const what = l.kind === 'agent' ? 'install' : l.kind === 'password' ? 'password' : 'join';
      return html`<tr><td><a href="${pathOf('members', l.member)}">${l.member}</a></td><td>${what}</td><td class="time">${time.until(l.expires_at)}</td><td class="r"><button class="btn sm danger-text" data-act="cancel-link" data-v="${String(l.id)}">Cancel</button></td></tr>`;
    });
    const storage = ps.map((p) => html`<tr role="button" tabindex="0" data-act="open" data-v="${p.name}"><td><span class="who">${Avatar.project(p.name, 'sm')}<span class="nm">${p.name}</span>${p.archived ? html`<span class="pill muted">archived</span>` : ''}</span></td><td class="num">${p.files}</td><td class="meter-cell">${Meter.of(p)}</td></tr>`);
    return html`<div class="tiles">
        ${tile('/members', 'People', n('person'), `${ms.filter((m) => m.kind === 'person' && !m.joined).length} not joined`)}
        ${tile('/members', 'Agents', n('agent'), `${ms.filter((m) => m.kind === 'agent' && m.enabled).length} active`)}
        ${tile('/members', 'Devices', n('device'), `${ms.filter((m) => m.kind === 'device' && m.enabled).length} active`)}
        ${tile('/projects', 'Projects', ps.length, `${ps.filter((p) => p.archived).length} archived`)}
        ${tile('/projects', 'Files', Bytes.format(total), `${files} files`)}
      </div>
      <div class="cards">
        ${this.card('To review', review.length ? html`<ul class="plain">${review.map((r) => html`<li>${Icon.of('warn')}${r}</li>`)}</ul>` : html`<p class="dim">Nothing.</p>`)}
        ${this.card('Links waiting', waiting.length ? html`<div class="tbl-wrap"><table><thead><tr><th>For</th><th>Link</th><th>Expires</th><th></th></tr></thead><tbody>${waiting}</tbody></table></div>` : html`<p class="dim">None.</p>`)}
      </div>
      ${this.card('Storage', html`<div class="tbl-wrap"><table class="rows"><thead><tr><th>Project</th><th class="num">Files</th><th>Used</th></tr></thead><tbody>${storage.length ? storage : html`<tr><td colspan="3" class="empty">No project yet.</td></tr>`}</tbody></table></div>`)}`;
  }
}

/** Settings: the configuration, which changes only in the file on the box. */
export class SettingsView extends View {
  protected view(): Html {
    const c = this.store.config;
    if (!c) return html`<p class="loading">Loading…</p>`;
    return html`<div class="cards">
      ${this.card('Listeners', html`<dl class="kv">
        <dt>listen</dt><dd><code>${c.listen}</code></dd>
        <dt>public-url</dt><dd><code>${c.public_url}</code></dd>
        <dt>admin-listen</dt><dd><code>${c.admin_listen}</code></dd>
        <dt>state-dir</dt><dd><code>${c.state_dir}</code></dd>
        <dt>Build</dt><dd><code>${c.version}</code></dd></dl>`, html`<span class="dim">/etc/cafard/config.yaml</span>`)}
      ${this.card('Limits', html`<dl class="kv">
        <dt>Largest upload</dt><dd>${Bytes.format(c.max_upload)}</dd>
        <dt>Link life</dt><dd>${c.link_hours} h, at most ${c.link_max_hours / 24} days</dd>
        <dt>Admin sign-in</dt><dd>${c.admin_signin_hours} h after last use</dd>
        <dt>Chat sign-in</dt><dd>${c.chat_signin_days} days after last use</dd></dl>`)}
    </div>`;
  }
}

/** The sign-in form, shown instead of the shell when nobody is signed in. */
export class SignInView extends Component {
  constructor(
    private readonly app: AdminApp,
    private readonly message?: string,
  ) {
    super('div', 'signin');
    this.root.addEventListener('submit', (e) => {
      e.preventDefault();
      void this.submit();
    });
  }

  protected view(): Html {
    return html`<form novalidate>
      <div class="brand">${Icon.logo} cafard <small>admin</small></div>
      ${this.message ? html`<p class="ferr" role="alert">${this.message}</p>` : ''}
      <input type="text" name="username" value="admin" autocomplete="username" hidden>
      <div class="field"><label for="p">Admin password</label><input class="input" id="p" type="password" autocomplete="current-password" required></div>
      <span class="ferr" id="sErr" hidden></span>
      <button class="btn filled" type="submit">Sign in</button></form>`;
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
