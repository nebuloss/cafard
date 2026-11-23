import { Component } from '@shared/component';
import { time } from '@shared/format';
import { Html, html } from '@shared/html';
import type { AdminApp } from './app';
import type { AdminStore } from './store';

/** A page of the admin UI, drawn from the store. */
export abstract class View extends Component {
  constructor(protected readonly app: AdminApp) {
    super('div', 'page');
  }

  /** What to review: machines unused for a month, links that expired unused. */
  protected review(): string[] {
    const members = this.store.members;
    return [
      ...members.filter((m) => m.last_used && time.daysSince(m.last_used) > 30).map((m) => `${m.name} unused for ${time.daysSince(m.last_used ?? '')} days`),
      ...members.filter((m) => !m.joined && m.link?.state === 'expired').map((m) => `the link for ${m.name} expired`),
    ];
  }

  protected get store(): AdminStore {
    return this.app.store;
  }

  protected head(title: string, text: string | Html, button?: Html): Html {
    return html`<div class="page-head"><div class="t"><h1>${title}</h1><p>${text}</p></div>${button ?? ''}</div>`;
  }
}
