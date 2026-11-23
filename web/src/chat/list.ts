import { Component } from '@shared/component';
import { time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { Avatar } from '@shared/widgets';
import type { ChatApp } from './app';
import type { Conversation } from './types';

/** When a conversation last moved, as the list shows it: 5 min, 14:02, Yesterday. */
function listTime(iso: string | undefined): string {
  if (!iso) return '';
  const m = (Date.now() - new Date(iso).getTime()) / 60000;
  if (m < 1) return 'now';
  if (m < 60) return `${Math.floor(m)} min`;
  const d = new Date(iso);
  if (d.toDateString() === new Date().toDateString()) return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  return time.when(iso).replace(/ \d\d:\d\d$/, '');
}

/**
 * ConversationList is the left pane: every conversation of every project,
 * grouped by project, most recently active first, closed ones last. Three
 * things on screen (docs/design.md, "The chat app"): projects as headers,
 * conversations as rows, and the members behind them.
 */
export class ConversationList extends Component {
  private query = '';

  constructor(private readonly app: ChatApp) {
    super('aside', 'side');
    this.actions = {
      new: () => this.app.go(`#new`),
      profile: () => this.app.openProfile(),
      open: (t) => this.app.go(`#c/${t.dataset['v'] ?? ''}`),
    };
    this.root.addEventListener('input', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.classList.contains('list-q')) {
        this.query = t.value.trim().toLowerCase();
        this.drawRows();
      }
    });
  }

  protected view(): Html {
    const me = this.app.store.me;
    return html`<div class="side-head"><div class="brand">${Icon.logo} cafard</div>
        <button class="icon-btn" data-act="profile" aria-label="Your profile" title="${me?.display_name || me?.name || ''}">${me ? Avatar.of(me, 'sm') : ''}</button>
        <button class="icon-btn filled" data-act="new" aria-label="New conversation" title="New conversation">${Icon.of('plus')}</button></div>
      <label class="search">${Icon.of('search')}<input class="list-q" type="search" placeholder="Search conversations" value="${this.query}" autocomplete="off"></label>
      <nav class="list" aria-label="Conversations"></nav>`;
  }

  protected override afterRender(): void {
    this.drawRows();
  }

  /** Draws the rows only, so the search keeps its focus. */
  drawRows(): void {
    const list = this.find('.list');
    if (!list) return;
    const current = this.app.currentAddress?.toString();
    const groups: Html[] = [];
    for (const p of this.app.store.projects) {
      const cs = (this.app.store.conversations.get(p.name) ?? [])
        .filter((c) => !this.query || c.title.toLowerCase().includes(this.query) || (c.last?.text ?? '').toLowerCase().includes(this.query))
        .sort((a, b) => Number(!!a.closed) - Number(!!b.closed) || (b.last_at ?? '').localeCompare(a.last_at ?? ''));
      if (!cs.length && this.query) continue;
      groups.push(html`<div class="group">${p.name}${p.archived ? ' · archived' : ''}</div>`);
      if (!cs.length) groups.push(html`<p class="row-empty">No conversation yet.</p>`);
      for (const c of cs) groups.push(this.row(p.name, c, c.address === current));
    }
    list.innerHTML = (groups.length ? Html.join(groups) : html`<p class="empty">${this.query ? 'No conversation matches.' : 'You are in no project yet. Ask an admin to add you to one.'}</p>`).value;
  }

  private row(project: string, c: Conversation, selected: boolean): Html {
    const last = c.last;
    const preview = last ? `${last.author === this.app.store.me?.name ? 'You' : last.author}: ${last.text || (last.files ? `${last.files} file${last.files === 1 ? '' : 's'}` : '')}` : 'No message yet';
    const tag = c.unread ? html`<span class="badge">${c.unread}</span>`
      : c.live ? html`<span class="pill live">live</span>`
      : c.closed ? html`<span class="pill muted">closed</span>` : Html.empty;
    return html`<button class="row ${c.closed ? 'closed' : ''}" data-act="open" data-v="${c.address}" aria-current="${String(selected)}">${Avatar.project(project)}
      <span class="row-main"><span class="row-top"><span class="row-title">${c.title}</span><span class="row-time">${listTime(c.last_at)}</span></span>
      <span class="row-bot"><span class="row-sub">${preview}</span>${tag}</span></span></button>`;
  }
}
