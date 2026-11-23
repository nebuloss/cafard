import { Component } from '@shared/component';
import { time } from '@shared/format';
import { Html, html } from '@shared/html';
import { ui } from '@shared/ui';
import type { AdminApp } from './app';
import type { AdminStore } from './store';

/**
 * A page of the admin UI, drawn from the store. A page has no title of its
 * own: the trail in the top bar names it. What it shows is in cards, each
 * with its title and its actions on one line, as claudication's are.
 */
export abstract class View extends Component {
  constructor(protected readonly app: AdminApp) {
    super('div', 'flex flex-col gap-4 pb-6');
  }

  protected get store(): AdminStore {
    return this.app.store;
  }

  /** What to review: machines unused for a month, links that expired unused. */
  protected review(): string[] {
    const members = this.store.members;
    return [
      ...members.filter((m) => m.last_used && time.daysSince(m.last_used) > 30).map((m) => `${m.name} unused for ${time.daysSince(m.last_used ?? '')} days`),
      ...members.filter((m) => !m.joined && m.link?.state === 'expired').map((m) => `the link for ${m.name} expired`),
    ];
  }

  /**
   * A page's cards in places rather than in a grid that fills itself: on a
   * wide screen, main is a wide column and side a narrow one beside it with
   * end under it; on a narrow one, one column in reading order: side (what
   * the item is), main (what it holds), end (what can be done to it, the
   * irreversible last). A self-filling grid put a short card beside a tall
   * one and left the hole under it.
   */
  protected layout(parts: { main: Html[]; side?: Html[]; end?: Html[] }): Html {
    const col = 'flex min-w-0 flex-col gap-4';
    return html`<div class="grid items-start gap-4 [grid-template-areas:'side'_'main'_'end'] lg:grid-cols-[minmax(0,1fr)_minmax(20rem,24rem)] lg:grid-rows-[auto_1fr] lg:[grid-template-areas:'main_side'_'main_end'] 2xl:grid-cols-[minmax(0,1fr)_28rem]">
      ${parts.side?.length ? html`<div class="${col} [grid-area:side]">${parts.side}</div>` : ''}
      <div class="${col} [grid-area:main]">${parts.main}</div>
      ${parts.end?.length ? html`<div class="${col} [grid-area:end]">${parts.end}</div>` : ''}</div>`;
  }

  /** A card: its title and actions on one line, then its body. */
  protected card(title: string | Html, body: Html, aside?: Html, extra = ''): Html {
    return html`<section class="${ui.card} ${extra}"><div class="${ui.cardHead}"><h2 class="${ui.cardTitle}">${title}</h2>${aside ? html`<div class="${ui.cardAside}">${aside}</div>` : ''}</div>${body}</section>`;
  }
}
