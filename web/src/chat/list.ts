import { Component } from '@shared/component';
import { time } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { ui } from '@shared/ui';
import { Avatar } from '@shared/widgets';
import type { ChatApp } from './app';
import { pathOf } from './route';
import { Address } from './store';
import { look } from './look';
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
    super('aside', 'flex min-h-0 min-w-0 flex-col border-r border-outline-variant bg-surface-low max-[840px]:group-data-[pane=chat]/app:hidden');
    this.actions = {
      new: () => this.app.go('/new'),
      profile: () => this.app.openProfile(),
      open: (t) => {
        const a = Address.parse(t.dataset['v'] ?? '');
        if (a) this.app.go(pathOf({ kind: 'conversation', project: a.project, name: a.name }));
      },
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
    return html`<div class="flex shrink-0 items-center gap-2.5 px-4 pt-3 pb-2"><div class="flex flex-1 items-center gap-2 text-lg font-bold tracking-[-.01em] [&_svg]:size-[26px] [&_svg]:text-primary">${Icon.logo} cafard</div>
        <button class="${ui.iconBtn}" data-act="profile" aria-label="Your profile" title="${me?.display_name || me?.name || ''}">${me ? Avatar.of(me, 'sm') : ''}</button>
        <button class="${look.iconBtnFilled}" data-act="new" aria-label="New conversation" title="New conversation">${Icon.of('plus')}</button></div>
      <label class="${look.search} mx-4 mb-2 h-[38px] shrink-0">${Icon.of('search')}<input class="list-q" type="search" placeholder="Search conversations" value="${this.query}" autocomplete="off"></label>
      <nav class="list flex-1 overflow-y-auto pb-4" aria-label="Conversations"></nav>`;
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
      groups.push(html`<div class="${look.sec}">${p.name}${p.archived ? ' · archived' : ''}</div>`);
      if (!cs.length) groups.push(html`<p class="pt-0.5 pr-4 pb-2.5 pl-[68px] text-[13px] text-on-surface-variant">No conversation yet.</p>`);
      for (const c of cs) groups.push(this.row(p.name, c, c.address === current));
    }
    list.innerHTML = (groups.length ? Html.join(groups) : html`<p class="${look.empty}">${this.query ? 'No conversation matches.' : 'You are in no project yet. Ask an admin to add you to one.'}</p>`).value;
  }

  private row(project: string, c: Conversation, selected: boolean): Html {
    const last = c.last;
    const preview = last ? `${last.author === this.app.store.me?.name ? 'You' : last.author}: ${last.text || (last.files ? `${last.files} file${last.files === 1 ? '' : 's'}` : '')}` : 'No message yet';
    const tag = c.unread ? html`<span class="grid h-5 min-w-5 shrink-0 place-items-center rounded-full bg-primary px-1.5 text-[11.5px] font-bold text-on-primary">${c.unread}</span>`
      : c.live ? html`<span class="${look.live}">live</span>`
      : c.closed ? html`<span class="${ui.pill.muted} shrink-0">closed</span>` : Html.empty;
    // The open conversation is in the primary container, its time and
    // preview in its own colour rather than the others' grey.
    const row = selected ? 'bg-primary-container text-on-primary-container' : 'hover:bg-surface-container';
    const dim = selected ? 'opacity-80' : 'text-on-surface-variant';
    return html`<button class="flex w-full items-center gap-3 px-4 py-[9px] text-left ${row}" data-act="open" data-v="${c.address}" aria-current="${String(selected)}">${Avatar.project(project)}
      <span class="min-w-0 flex-1"><span class="flex items-baseline gap-2"><span class="min-w-0 flex-1 truncate ${c.closed ? 'font-medium opacity-70' : 'font-semibold'}">${c.title}</span><span class="shrink-0 text-xs tabular-nums ${dim}">${listTime(c.last_at)}</span></span>
      <span class="mt-0.5 flex items-center gap-1.5"><span class="min-w-0 flex-1 truncate text-[13px] ${dim}">${preview}</span>${tag}</span></span></button>`;
  }
}
