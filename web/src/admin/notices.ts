import { Component } from '@shared/component';
import { time } from '@shared/format';
import { Html, html } from '@shared/html';
import { n, t } from '@shared/i18n';
import { Icon, type IconName } from '@shared/icons';
import { ui } from '@shared/ui';
import type { AdminApp } from './app';
import { closeButton } from './pickers';
import type { Notice } from './types';

/**
 * The notification centre, as claudication's (web/src/ui/features/notices
 * there): a bell in the top bar counting what needs the admin, opening a
 * side sheet of it, grouped by what it is about, each a link to where it is
 * dealt with. The notices are worked out by the server from the state; one
 * marked read leaves the bell and the sheet until its problem is gone and
 * comes back.
 */

type Severity = Notice['severity'];

const TONE: Record<Severity, { icon: IconName; circle: string; badge: string }> = {
  error: { icon: 'error', circle: 'bg-error-container text-on-error-container', badge: 'bg-error text-on-error' },
  warn: { icon: 'warn', circle: 'bg-warning-container text-on-warning-container', badge: 'bg-warning text-on-warning' },
  info: { icon: 'info', circle: 'bg-info-container text-on-info-container', badge: 'bg-info-container text-on-info-container' },
};

/** What a severity's chip says. */
function toneLabel(s: Severity | 'all'): string {
  switch (s) {
    case 'all':
      return t('All');
    case 'error':
      return t('Errors');
    case 'warn':
      return t('Warnings');
    case 'info':
      return t('Info');
  }
}

const GROUPS: Notice['kind'][] = ['member', 'project'];

function groupLabel(kind: Notice['kind']): string {
  return kind === 'member' ? t('Members') : t('Projects');
}

/** The bell: the unread count, in the colour of the worst one. */
export function bell(notices: Notice[]): Html {
  const count = notices.length;
  const worst = (['error', 'warn', 'info'] as const).find((s) => notices.some((x) => x.severity === s));
  const label = count ? n(count, '{n} notification', '{n} notifications') : t('Notifications');
  return html`<button class="state-layer relative grid size-10 shrink-0 place-items-center rounded-full border border-outline text-on-surface-variant" data-act="notices" aria-label="${label}" title="${label}" aria-haspopup="dialog">${Icon.of('bell')}${
    count && worst ? html`<span class="absolute -top-1 -right-1 grid h-5 min-w-5 place-items-center rounded-full px-1 text-[11px] font-semibold tabular-nums ${TONE[worst].badge}">${count > 99 ? '99+' : count}</span>` : ''
  }</button>`;
}

/** The side sheet of notices. */
export class NoticeCenter extends Component {
  private filter: Severity | 'all' = 'all';

  constructor(private readonly app: AdminApp) {
    super('aside');
    this.actions = {
      close: () => this.app.layer.close(),
      filter: (el) => {
        this.filter = (el.dataset['v'] ?? 'all') as Severity | 'all';
        this.render();
      },
      read: async (el) => {
        await this.app.attempt(() => this.app.store.markRead([el.dataset['v'] ?? '']));
        this.changed();
      },
      'read-all': async () => {
        await this.app.attempt(() => this.app.store.markRead(this.shown().map((x) => x.id)));
        this.changed();
      },
      go: (el) => void this.app.go(el.dataset['v'] ?? '/'),
    };
  }

  private shown(): Notice[] {
    return this.app.store.unread().filter((x) => this.filter === 'all' || x.severity === this.filter);
  }

  /** A notice was marked: this sheet and the bell show it gone. */
  private changed(): void {
    this.render();
    this.app.redrawBell();
  }

  protected view(): Html {
    const unread = this.app.store.unread();
    const head = html`<div class="${ui.dialogHead}"><div class="min-w-0 flex-1"><h2>${t('Notifications')}</h2></div>${closeButton()}</div>`;
    if (!unread.length) {
      return html`${head}<div class="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
        <span class="grid size-14 place-items-center rounded-full bg-success-container text-on-success-container [&_.ic]:size-7">${Icon.of('check')}</span>
        <span class="text-base">${t('Nothing needs you')}</span></div>`;
    }
    const count = (s: Severity | 'all') => unread.filter((x) => s === 'all' || x.severity === s).length;
    const chips = (['all', 'error', 'warn', 'info'] as const).filter((s) => s === 'all' || count(s)).map((s) => {
      const on = this.filter === s;
      return html`<button class="state-layer flex h-8 items-center gap-1.5 rounded-md3-s px-3 text-xs font-medium ${on ? 'bg-secondary-container text-on-secondary-container' : 'text-on-surface-variant ring-1 ring-outline-variant ring-inset'}" role="radio" aria-checked="${String(on)}" data-act="filter" data-v="${s}">${toneLabel(s)}<span class="tabular-nums opacity-70">${count(s)}</span></button>`;
    });
    const shown = this.shown();
    const groups = GROUPS.map((kind) => {
      const items = shown.filter((x) => x.kind === kind);
      if (!items.length) return Html.empty;
      const label = groupLabel(kind);
      return html`<section aria-label="${label}"><h3 class="mb-2 flex items-center gap-2 text-xs font-semibold tracking-wide text-on-surface-variant uppercase">${label}<span class="tabular-nums">${items.length}</span></h3>
        <ul class="m-0 flex list-none flex-col gap-2 p-0">${items.map((x) => this.row(x))}</ul></section>`;
    });
    return html`${head}<div class="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-6 pb-6">
      <div class="flex flex-wrap items-center gap-2"><div class="flex flex-1 flex-wrap gap-2" role="radiogroup" aria-label="${t('Show')}">${chips}</div>
        <button class="${ui.btn('text', 'sm')}" data-act="read-all">${Icon.of('read')}${t('Mark all as read')}</button></div>
      ${groups}</div>`;
  }

  private row(x: Notice): Html {
    const tone = TONE[x.severity];
    const body = html`<span class="grid size-9 shrink-0 place-items-center rounded-full ${tone.circle} [&_.ic]:size-5">${Icon.of(tone.icon)}</span>
      <span class="flex min-w-0 flex-1 flex-col gap-1"><span class="text-sm font-medium text-on-surface">${x.title}</span>
        ${x.detail ? html`<span class="line-clamp-3 text-xs text-on-surface-variant">${x.detail}</span>` : ''}
        ${x.since ? html`<span class="text-xs text-on-surface-variant tabular-nums">${time.ago(x.since)}</span>` : ''}</span>
      ${x.link ? html`<span class="mt-2 text-on-surface-variant">${Icon.of('chev')}</span>` : ''}`;
    const main = x.link
      ? html`<button class="state-layer flex min-w-0 flex-1 items-start gap-3 rounded-md3-l p-3 text-left" data-act="go" data-v="${x.link}">${body}</button>`
      : html`<div class="flex min-w-0 flex-1 items-start gap-3 p-3">${body}</div>`;
    return html`<li class="flex items-start rounded-md3-l bg-surface-lowest">${main}
      <button class="state-layer mt-2.5 mr-2 grid size-9 shrink-0 place-items-center rounded-full text-on-surface-variant [&_.ic]:size-5" data-act="read" data-v="${x.id}" aria-label="${t('Mark as read')}" title="${t('Mark as read')}">${Icon.of('read')}</button></li>`;
  }
}
