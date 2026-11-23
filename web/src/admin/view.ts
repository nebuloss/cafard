import { Component } from '@shared/component';
import { Html, html } from '@shared/html';
import { ui } from '@shared/ui';
import type { AdminApp } from './app';
import type { AdminStore } from './store';

/** A figure on a tile: its label, its value, and a hint under it. */
export interface Stat {
  label: string;
  value: string | number;
  hint?: string;
  /** Where the tile leads, if anywhere. */
  href?: string;
}

/**
 * A page of the admin UI, drawn from the store. A page has no title of its
 * own: the trail in the top bar names it. It is one column of full-width
 * cards, a step apart, each with its title and its actions on one line, as
 * claudication's pages are; what sits side by side does so inside a card
 * (a row of stats, a table), never as two columns of cards, which leave a
 * hole under the shorter one.
 */
export abstract class View extends Component {
  constructor(protected readonly app: AdminApp) {
    super('div', `${ui.page} pb-6`);
  }

  protected get store(): AdminStore {
    return this.app.store;
  }

  /** A card: its title and actions on one line, then its body. */
  protected card(title: string | Html, body: Html, aside?: Html, extra = ''): Html {
    return html`<section class="${ui.card} ${extra}"><div class="${ui.cardHead}"><h2 class="${ui.cardTitle}">${title}</h2>${aside ? html`<div class="${ui.cardAside}">${aside}</div>` : ''}</div>${body}</section>`;
  }

  /** A row of stat tiles. */
  protected stats(items: Stat[]): Html {
    const tile = (s: Stat) => {
      const inner = html`<span class="${ui.overline}">${s.label}</span><span class="${ui.statValue}">${s.value}</span>${s.hint ? html`<span class="${ui.statHint}">${s.hint}</span>` : ''}`;
      return s.href ? html`<a class="${ui.stat} state-layer" href="${s.href}">${inner}</a>` : html`<div class="${ui.stat}">${inner}</div>`;
    };
    return html`<div class="${ui.statGrid[items.length] ?? ui.statGrid[4]}">${items.map(tile)}</div>`;
  }

  /**
   * An item's header: its mark and name, a line under them, and its state at
   * a glance on the right, the switch that changes it among it.
   */
  protected itemHead(avatar: Html, title: string | Html, sub: string, aside?: Html): Html {
    return html`<div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
      <div class="flex min-w-0 items-center gap-3">${avatar}<div class="min-w-0"><h1 class="m-0 text-2xl leading-8 font-normal break-all">${title}</h1><p class="mt-0.5 text-sm text-on-surface-variant">${sub}</p></div></div>
      ${aside ? html`<div class="flex flex-wrap items-center gap-3">${aside}</div>` : ''}</div>`;
  }
}
