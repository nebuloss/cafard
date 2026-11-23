import { Component } from '@shared/component';
import { Bytes, time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { Avatar, Meter, Switch } from '@shared/widgets';
import type { AdminApp } from './app';
import { CreateMemberFlow, CreateProjectFlow } from './create';
import { AdminStore } from './store';
import type { AuditEntry, Member } from './types';

/** A page of the admin UI, drawn from the store. */
export abstract class View extends Component {
  constructor(protected readonly app: AdminApp) {
    super('div', 'page');
  }

  protected get store(): AdminStore {
    return this.app.store;
  }

  protected head(title: string, text: string | Html, button?: Html): Html {
    return html`<div class="page-head"><div class="t"><h1>${title}</h1><p>${text}</p></div>${button ?? ''}</div>`;
  }
}

/** How a member's row describes them under their name. */
function subOf(m: Member): string {
  if (m.kind === 'person') return `@${m.name}${m.joined && m.signins ? ` · ${m.signins} sign-in${m.signins === 1 ? '' : 's'}` : ''}`;
  if (m.kind === 'agent') return m.description || 'No description: say what this machine can do.';
  return `Sends only${m.description ? ' · ' + m.description : ''}`;
}

function linkPill(m: Member): Html {
  if (m.joined || !m.link) return Html.empty;
  switch (m.link.state) {
    case 'waiting': return html`<span class="pill warn">link waiting</span>`;
    case 'expired': return html`<span class="pill err">link expired</span>`;
    case 'cancelled': return html`<span class="pill muted">link cancelled</span>`;
    default: return Html.empty;
  }
}

export class MembersView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      new: () => this.app.layer.open(new CreateMemberFlow(this.app), 'center'),
      filter: (t) => {
        this.app.ui.kind = t.dataset['v'] ?? 'all';
        this.render();
      },
      open: (t) => this.app.openMember(t.dataset['v'] ?? ''),
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
        this.drawList();
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
    const stale = members.filter((m) => m.last_used && time.daysSince(m.last_used) > 30);
    const lost = members.filter((m) => !m.joined && m.link?.state === 'expired');
    const review = [
      ...stale.map((m) => `${m.name} unused for ${time.daysSince(m.last_used ?? '')} days`),
      ...lost.map((m) => `the link for ${m.name} expired`),
    ];
    const seg = (['all', 'person', 'agent', 'device'] as const).map((k) =>
      html`<button aria-pressed="${String(this.app.ui.kind === k)}" data-act="filter" data-v="${k}">${{ all: 'All', person: 'People', agent: 'Agents', device: 'Devices' }[k]} ${count(k)}</button>`);
    return html`${this.head('People, agents and devices',
      'Everyone who can reach cafard. People sign in, agents connect with a token their install link gave them, devices only send. What each can see comes from its projects.',
      html`<button class="btn filled" data-act="new">${Icon.of('plus')}New</button>`)}
      ${review.length ? html`<div class="notice">${Icon.of('warn')}<p><b>${review.length} to review.</b> ${review.join(' · ')}.</p></div>` : ''}
      <div class="toolbar"><label class="search">${Icon.of('search')}<input id="q" type="search" placeholder="Search by name, description or project" value="${this.app.ui.query}" autocomplete="off"></label><div class="seg-wrap"><div class="seg" role="group" aria-label="Show">${seg}</div></div></div>
      <div class="list"></div>`;
  }

  protected override afterRender(): void {
    this.drawList();
  }

  /** Draws the rows only, so the search keeps its focus while typing. */
  private drawList(): void {
    const rows = this.rows().map((m) => {
      const tags = html`${m.kind !== 'person' ? html`<span class="kind ${m.kind}">${m.kind}</span>` : ''}${linkPill(m)}${!m.enabled ? html`<span class="pill muted">paused</span>` : ''}`;
      const last = m.joined || m.kind === 'device' ? html`<b>${time.ago(m.last_used)}</b>${m.last_from ?? ''}` : html`<b>Not joined</b>`;
      const chips = m.projects.length ? m.projects.map((p) => html`<span class="chip">${p}</span>`) : html`<span class="sub">No project</span>`;
      return html`<div class="irow ${m.enabled ? '' : 'off'}" role="button" tabindex="0" data-act="open" data-v="${m.name}">${Avatar.of(m)}
        <span class="who"><span class="nm">${m.display_name || m.name}${tags}</span><span class="sub">${subOf(m)}</span></span>
        <span class="chips">${chips}</span><span class="last">${last}</span>
        ${Switch.of(m.enabled, 'pause', m.name, `${m.enabled ? 'Pause' : 'Resume'} ${m.name}`)}<span class="chev">${Icon.of('chev')}</span></div>`;
    });
    this.$('.list').innerHTML = (rows.length ? Html.join(rows) : html`<div class="empty">Nobody matches.</div>`).value;
  }
}

export class ProjectsView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      'new-project': () => this.app.layer.open(new CreateProjectFlow(this.app), 'center'),
      proj: (t) => this.app.openProject(t.dataset['v'] ?? ''),
    };
  }

  protected view(): Html {
    const ps = [...this.store.projects].sort((a, b) => Number(!!a.archived) - Number(!!b.archived) || a.name.localeCompare(b.name));
    const rows = ps.map((p) => {
      const stack = html`${p.members.slice(0, 5).map((m) => Avatar.of(m))}${p.members.length > 5 ? html`<span class="more">+${p.members.length - 5}</span>` : ''}`;
      return html`<button class="prow ${p.archived ? 'archived' : ''}" data-act="proj" data-v="${p.name}">${Avatar.project(p.name)}
        <span class="who"><span class="nm">${p.name}${p.archived ? html`<span class="pill muted">archived · read-only</span>` : ''}</span>
        <span class="sub">${p.conversations} conversations · ${p.files} files · ${p.members.length} members</span></span>
        <span class="stack">${stack}</span>${Meter.of(p)}<span class="chev">${Icon.of('chev')}</span></button>`;
    });
    return html`${this.head('Projects', 'A project decides who sees what: its people and agents read and write all its conversations, devices only send to it. People already in a project can add members from the chat too.',
      html`<button class="btn filled" data-act="new-project">${Icon.of('plus')}New project</button>`)}
      <div class="list">${rows.length ? rows : html`<div class="empty">No project yet. Make one, then put people and agents in it.</div>`}</div>`;
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
      html`<tr><td class="time">${time.when(e.at)}</td><td><b>${e.actor}</b> ${e.action} <b>${e.target}</b></td><td><span class="mono">${e.origin}</span></td></tr>`);
    return html`${this.head('Audit log', 'Every change to who can reach what, from this UI, from the chat and from cafard admin on the box: who did it, when, and from where.')}
      <div class="toolbar"><div class="seg-wrap"><div class="seg" role="group" aria-label="Show">${seg}</div></div></div>
      <div class="tbl-wrap"><table><thead><tr><th>When</th><th>What</th><th>From</th></tr></thead><tbody>${rows.length ? rows : html`<tr><td colspan="3" class="empty">Nothing yet.</td></tr>`}</tbody></table></div>`;
  }
}

export class StorageView extends View {
  constructor(app: AdminApp) {
    super(app);
    this.actions = {
      proj: (t) => this.app.openProject(t.dataset['v'] ?? ''),
      'cancel-link': async (t) => {
        await this.app.change(() => this.store.api.delete(`/links/${t.dataset['v']}`), 'The link no longer works.');
      },
    };
  }

  protected view(): Html {
    const ps = this.store.projects;
    const total = ps.reduce((s, p) => s + p.bytes, 0);
    const files = ps.reduce((s, p) => s + p.files, 0);
    const waiting = this.store.links.filter((l) => l.state === 'waiting').map((l) => {
      const what = l.kind === 'agent' ? 'install link' : l.kind === 'password' ? 'password link' : 'join link';
      return html`<div class="line"><div class="m"><b>${l.member}</b><span>${what} · made by ${l.created_by} ${time.ago(l.created_at)} · ${time.until(l.expires_at)}</span></div><button class="btn sm danger-text" data-act="cancel-link" data-v="${String(l.id)}">Cancel</button></div>`;
    });
    const rows = ps.map((p) => html`<button class="prow storage" data-act="proj" data-v="${p.name}">${Avatar.project(p.name)}<span class="who"><span class="nm">${p.name}${p.archived ? html`<span class="pill muted">archived</span>` : ''}</span><span class="sub">${p.files} files</span></span>${Meter.of(p)}<span class="chev">${Icon.of('chev')}</span></button>`);
    return html`${this.head('Storage', 'Files are kept per project for its retention; messages stay when files expire.')}
      <div class="grid2">
        <div class="card"><h2>Files</h2><div class="big">${Bytes.format(total)} <small>in ${files} files</small></div><p>Counted per project: the same file sent to two projects counts in each, though it is stored once.</p></div>
        <div class="card"><h2>Links waiting</h2>${waiting.length ? waiting : html`<p>No link is waiting to be used.</p>`}</div>
      </div>
      <h2 class="h2-gap">By project</h2>
      <div class="list">${rows.length ? rows : html`<div class="empty">No project yet.</div>`}</div>`;
  }
}

export class ConfigView extends View {
  protected view(): Html {
    const c = this.store.config;
    if (!c) return html`<p class="loading">Loading…</p>`;
    return html`${this.head('Configuration', html`Where cafard listens and what it allows. These come from <code>/etc/cafard/config.yaml</code> and change only there, then a restart.`)}
      <div class="grid2">
        <div class="card"><h2>Listeners</h2>
          <div class="listen"><code>listen ${c.listen}</code><span>Published: the chat app, /api/v1, join links. People and agents reach it at <code>${c.public_url}</code>.</span></div>
          <div class="listen"><code>admin-listen ${c.admin_listen}</code><span>This UI. The public listener answers 404 to every /admin path.</span></div></div>
        <div class="card"><h2>Limits</h2><dl class="kv">
          <dt>Largest upload</dt><dd>${Bytes.format(c.max_upload)}</dd>
          <dt>Links wait, by default</dt><dd>${c.link_hours} hours</dd>
          <dt>Links wait, at most</dt><dd>${c.link_max_hours / 24} days</dd>
          <dt>An admin sign-in lasts</dt><dd>${c.admin_signin_hours} hours from its last use</dd>
          <dt>A chat sign-in lasts</dt><dd>${c.chat_signin_days} days from its last use</dd>
          <dt>State directory</dt><dd><code>${c.state_dir}</code></dd>
          <dt>Build</dt><dd><code>${c.version}</code></dd></dl></div>
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
      <h1>Sign in</h1>
      <p>With the admin password. It is set on the box with <code>cafard passwd</code>, which is also how a lost one is replaced.</p>
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

