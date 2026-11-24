import { Component } from '@shared/component';
import { time } from '@shared/format';
import { Html, html } from '@shared/html';
import { i18n, n, t } from '@shared/i18n';
import { Icon } from '@shared/icons';
import { ui } from '@shared/ui';
import type { AdminApp } from './app';
import { AuditFilter, fieldLabel, valueLabel, type AuditField } from './audit-filter';
import { closeButton } from './pickers';
import type { AuditEntry } from './types';
import { View } from './view';

/**
 * The audit log, as claudication's request log (web/src/ui/features/requests
 * there): a table filling its card, newest first, read a page at a time.
 *
 * - A column's heading opens a menu of the values it has, with how many
 *   entries have each; ticking some narrows the log to them.
 * - A value in a row narrows to it with one click.
 * - What is narrowed is said once, as chips above the table, each removable:
 *   a filtered log that looks like the whole one is how someone concludes
 *   nothing happened.
 * - The filter is in the URL (audit-filter.ts), so it is a link, and Back
 *   undoes a narrowing.
 * - A row opens the whole entry.
 */

const KIND_PILL: Record<string, string> = {
  members: 'bg-secondary-container text-on-secondary-container',
  projects: 'bg-tertiary-container text-on-tertiary-container',
  links: 'bg-warning-container text-on-warning-container',
  'sign-ins': 'bg-info-container text-on-info-container',
  settings: 'bg-surface-container-highest text-on-surface',
};

/** A value that narrows the log to itself when clicked. */
function narrowing(field: AuditField, value: string, inner: Html | string, extra = ''): Html {
  return html`<button class="max-w-full truncate rounded-md3-xs text-left hover:underline ${extra}" data-act="narrow" data-f="${field}" data-v="${value}" title="${t('Only {field}: {value}', { field: fieldLabel(field).toLowerCase(), value: valueLabel(field, value) })}">${inner}</button>`;
}

function kindPill(kind: string): Html {
  return html`<span class="inline-flex h-5 items-center rounded-full px-2 text-[11.5px] font-semibold whitespace-nowrap ${KIND_PILL[kind] ?? KIND_PILL['members']}">${valueLabel('kind', kind)}</span>`;
}

/** The address an origin carries after its source, if any. */
function address(e: AuditEntry): string {
  const i = e.origin.indexOf(' · ');
  return i >= 0 ? e.origin.slice(i + 3) : '';
}

export class AuditView extends View {
  private filter: AuditFilter;
  /** The column whose menu is open. */
  private menu: AuditField | null = null;
  private loadingMore = false;
  private readonly outside = (e: MouseEvent) => {
    if (!this.root.isConnected) return document.removeEventListener('click', this.outside);
    const el = e.target as Element | null;
    if (this.menu && !el?.closest('[data-menu], [data-act="menu"]')) {
      this.menu = null;
      this.render();
    }
  };

  constructor(app: AdminApp) {
    super(app);
    this.filter = AuditFilter.fromSearch(location.search);
    document.addEventListener('click', this.outside);
    this.actions = {
      menu: (el) => {
        const f = el.dataset['f'] as AuditField;
        this.menu = this.menu === f ? null : f;
        this.render();
      },
      toggle: (el) => this.narrow(this.filter.toggled(el.dataset['f'] as AuditField, el.dataset['v'] ?? ''), true),
      clear: (el) => this.narrow(this.filter.cleared(el.dataset['f'] as AuditField)),
      narrow: (el) => this.narrow(this.filter.only(el.dataset['f'] as AuditField, el.dataset['v'] ?? '')),
      unpin: (el) => this.narrow(this.filter.toggled(el.dataset['f'] as AuditField, el.dataset['v'] ?? '')),
      'clear-all': () => this.narrow(AuditFilter.empty()),
      more: async () => {
        this.loadingMore = true;
        this.render();
        await this.app.attempt(() => this.store.moreAudit(this.filter));
        this.loadingMore = false;
        this.render();
      },
      open: (el) => {
        const e = this.store.audit.find((x) => String(x.id) === el.dataset['v']);
        if (e) this.app.layer.open(new AuditDetail(this.app, e), 'center');
      },
    };
  }

  /** Narrows the log: a new entry in the history, then the log read again. */
  private async narrow(next: AuditFilter, keepMenu = false): Promise<void> {
    this.filter = next;
    if (!keepMenu) this.menu = null;
    history.pushState(null, '', next.href());
    await this.app.attempt(() => this.store.loadAudit(next));
    this.render();
  }

  protected view(): Html {
    const f = this.filter;
    const head = (field: AuditField | null, label: string, extra = '') => {
      if (!field) return html`<th class="${ui.th} ${extra}">${label}</th>`;
      const on = f.narrows(field);
      return html`<th class="${ui.th} ${extra} p-0">
        <button class="state-layer flex w-full items-center gap-1.5 px-3 py-2.5 uppercase ${on ? 'text-primary' : ''}" data-act="menu" data-f="${field}" aria-haspopup="menu" aria-expanded="${String(this.menu === field)}">${label}
          ${on ? html`<span class="grid size-4 place-items-center rounded-full bg-primary text-[10px] text-on-primary">${f.chosen(field).length}</span>` : ''}
          <svg class="h-2 w-3 shrink-0 fill-current opacity-60" viewBox="0 0 12 8" aria-hidden="true"><path d="M1 1l5 5 5-5"/></svg></button></th>`;
    };
    const rows = this.store.audit.map((e) => html`<tr class="${ui.trLink}" data-act="open" data-v="${String(e.id)}" role="button" tabindex="0">
      <td class="${ui.td} whitespace-nowrap text-[13px] text-on-surface-variant tabular-nums" title="${new Date(e.at).toLocaleString(i18n.locale)}">${time.when(e.at)}</td>
      <td class="${ui.td} font-semibold">${narrowing('actor', e.actor, e.actor)}</td>
      <td class="${ui.td}">${narrowing('kind', e.kind, kindPill(e.kind))}</td>
      <td class="${ui.td} max-w-[28rem]"><span class="line-clamp-2">${e.action}</span></td>
      <td class="${ui.td} font-semibold">${narrowing('target', e.target, e.target)}</td>
      <td class="${ui.td} whitespace-nowrap">${narrowing('source', e.source, valueLabel('source', e.source))}<span class="ml-2 font-mono text-xs text-on-surface-variant">${address(e)}</span></td></tr>`);
    const chips = f.pinned().map(({ field, value }) => html`<span class="inline-flex h-8 items-center gap-1 rounded-md3-s border border-outline-variant bg-surface-lowest pr-1 pl-3 text-[13px]">${t('{field}:', { field: fieldLabel(field) })} <b class="font-semibold">${valueLabel(field, value)}</b>
      <button class="state-layer grid size-6 place-items-center rounded-full text-on-surface-variant [&_.ic]:size-3.5" data-act="unpin" data-f="${field}" data-v="${value}" aria-label="${t('Stop filtering by {value}', { value: valueLabel(field, value) })}">${Icon.of('close')}</button></span>`);
    const narrowed = chips.length
      ? html`<div class="flex flex-wrap items-center gap-2 text-sm"><span class="text-on-surface-variant">${t('Narrowed to')}</span>${chips}<button class="${ui.btn('text', 'sm')}" data-act="clear-all">${t('Clear')}</button></div>`
      : Html.empty;
    const shown = this.store.audit.length;
    const whole = f.empty
      ? n(shown, 'That is the whole log: {n} entry.', 'That is the whole log: {n} entries.')
      : n(shown, 'That is the whole log for this filter: {n} entry.', 'That is the whole log for this filter: {n} entries.');
    const foot = this.store.auditNext
      ? html`<div class="flex items-center gap-3"><button class="${ui.btn('tonal', 'sm')}" data-act="more" ${Html.raw(this.loadingMore ? 'disabled' : '')}>${this.loadingMore ? t('Loading…') : t('Load more')}</button><span class="text-xs text-on-surface-variant">${n(shown, '{n} entry shown', '{n} entries shown')}</span></div>`
      : shown ? html`<p class="text-xs text-on-surface-variant">${whole}</p>` : Html.empty;
    const table = html`<div class="${ui.tableWrap} min-h-0 flex-1 overflow-auto"><table class="${ui.table} min-w-[52rem]"><thead class="sticky top-0 z-10 bg-surface-lowest shadow-[0_1px_0_var(--color-outline-variant)]"><tr>
        ${head(null, t('When'))}${head('actor', fieldLabel('actor'))}${head('kind', fieldLabel('kind'))}${head(null, t('What'))}${head('target', fieldLabel('target'))}${head('source', fieldLabel('source'))}</tr></thead>
      <tbody>${rows.length ? rows : html`<tr><td colspan="6" class="${ui.empty}">${f.empty ? t('Nothing yet.') : t('Nothing matches this filter.')}</td></tr>`}</tbody></table></div>`;
    return html`${this.card(t('Audit log'), html`${narrowed}${table}${foot}`, undefined, 'h-full min-h-0')}${this.menuView()}`;
  }

  /** The open column's menu: its values with their counts, to tick. */
  private menuView(): Html {
    const field = this.menu;
    if (!field) return Html.empty;
    const values = this.store.auditFacets[field] ?? [];
    const rows = values.map(({ value, count }) => {
      const on = this.filter.has(field, value);
      return html`<button class="state-layer flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm" role="menuitemcheckbox" aria-checked="${String(on)}" data-act="toggle" data-f="${field}" data-v="${value}">
        <span class="grid size-[18px] shrink-0 place-items-center rounded-[4px] border-2 ${on ? 'border-primary bg-primary text-on-primary' : 'border-outline'} [&_.ic]:size-3 [&_.ic]:stroke-[3]">${on ? Icon.of('check') : ''}</span>
        <span class="min-w-0 flex-1 truncate">${valueLabel(field, value)}</span><span class="text-xs text-on-surface-variant tabular-nums">${count}</span></button>`;
    });
    return html`<div class="fixed z-30 flex max-h-80 w-64 flex-col overflow-hidden rounded-md3-m bg-surface-lowest elevation-2" data-menu role="menu" aria-label="${fieldLabel(field)}">
      <div class="flex items-center justify-between border-b border-outline-variant px-3 py-2 text-xs font-semibold tracking-wider text-on-surface-variant uppercase">${fieldLabel(field)}${this.filter.narrows(field) ? html`<button class="normal-case tracking-normal text-primary hover:underline" data-act="clear" data-f="${field}">${t('Clear')}</button>` : ''}</div>
      <div class="min-h-0 overflow-y-auto py-1">${rows.length ? rows : html`<p class="px-3 py-2 text-sm text-on-surface-variant">${t('No values yet.')}</p>`}</div></div>`;
  }

  protected override afterRender(): void {
    super.afterRender();
    // The card is the window's height, and its table scrolls inside it.
    this.root.classList.add('h-full', 'pb-4');
    // The menu is fixed, outside the table's scroll, under its heading: a
    // menu inside the scrolling table would be cut by it.
    const menu = this.find('[data-menu]');
    const anchor = this.menu ? this.find(`[data-act="menu"][data-f="${this.menu}"]`) : null;
    if (menu && anchor) {
      const r = anchor.getBoundingClientRect();
      const left = Math.max(8, Math.min(r.left, window.innerWidth - 264));
      menu.style.top = `${r.bottom + 4}px`;
      menu.style.left = `${left}px`;
    }
  }
}

/** One entry, whole: what a row is too narrow to say. */
class AuditDetail extends Component {
  constructor(
    private readonly app: AdminApp,
    private readonly e: AuditEntry,
  ) {
    super('div');
    this.actions = { close: () => this.app.layer.close() };
  }

  protected view(): Html {
    const e = this.e;
    return html`<div class="${ui.dialogHead}"><div class="min-w-0 flex-1"><h2>${e.actor} ${e.action} ${e.target}</h2><p>${new Date(e.at).toLocaleString(i18n.locale)}</p></div>${closeButton()}</div>
      <div class="${ui.dialogBody}"><dl class="${ui.kv}">
        <dt>${fieldLabel('actor')}</dt><dd>${e.actor}</dd>
        <dt>${t('Did')}</dt><dd>${e.action}</dd>
        <dt>${fieldLabel('target')}</dt><dd>${e.target}</dd>
        <dt>${fieldLabel('kind')}</dt><dd>${kindPill(e.kind)}</dd>
        <dt>${fieldLabel('source')}</dt><dd>${valueLabel('source', e.source)}${address(e) ? html` <span class="font-mono text-xs">${address(e)}</span>` : ''}</dd>
        <dt>${t('When')}</dt><dd class="tabular-nums">${e.at}</dd>
        <dt>${t('Entry')}</dt><dd class="tabular-nums">#${e.id}</dd></dl></div>`;
  }
}

