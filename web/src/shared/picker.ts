import { Component } from './component';
import { Html, html } from './html';
import { Icon } from './icons';
import { ui } from './ui';

/** Something the picker can tick: a member or a project. */
export interface PickItem {
  id: string;
  label: string;
  /** person, agent or device; absent for projects. */
  kind?: string;
  sub: string;
  avatar: Html;
  /** What the search matches, lower case. */
  text: string;
  disabled?: boolean;
}

export type KindFilter = 'all' | 'person' | 'agent' | 'device';

/**
 * PickerState is the member picker without a page (docs/design.md, "The
 * admin UI"): which items are ticked, which were ticked when it opened, the
 * search and the kind filter, and what saving would change.
 *
 * Ticked items come first, then by name, in an order fixed when the picker
 * opens: re-sorting on every tick would move the row out from under the
 * pointer.
 */
export class PickerState {
  readonly selected: Set<string>;
  readonly initial: ReadonlySet<string>;
  kind: KindFilter = 'all';
  query = '';
  private readonly order: Map<string, number>;

  constructor(
    readonly items: PickItem[],
    selected: Iterable<string>,
    initial: Iterable<string> = [],
  ) {
    this.selected = new Set(selected);
    this.initial = new Set(initial);
    const sorted = [...items].sort(
      (a, b) => Number(this.selected.has(b.id)) - Number(this.selected.has(a.id)) || a.label.localeCompare(b.label),
    );
    this.order = new Map(sorted.map((it, i) => [it.id, i]));
  }

  toggle(id: string): void {
    const item = this.items.find((it) => it.id === id);
    if (!item || item.disabled) return;
    if (this.selected.has(id)) this.selected.delete(id);
    else this.selected.add(id);
  }

  /** The items the search and the filter let through, in the fixed order. */
  visible(): PickItem[] {
    const q = this.query.trim().toLowerCase();
    return this.items
      .filter((it) => (this.kind === 'all' || it.kind === this.kind) && (!q || it.text.includes(q)))
      .sort((a, b) => (this.order.get(a.id) ?? 0) - (this.order.get(b.id) ?? 0));
  }

  /** The first item that can be ticked: what Enter in the search ticks. */
  first(): PickItem | undefined {
    return this.visible().find((it) => !it.disabled);
  }

  changes(): { add: string[]; remove: string[] } {
    return {
      add: [...this.selected].filter((id) => !this.initial.has(id)),
      remove: [...this.initial].filter((id) => !this.selected.has(id)),
    };
  }

  /** What a row says about itself: will be added, will be removed, member. */
  tag(id: string): 'add' | 'remove' | 'member' | null {
    const on = this.selected.has(id);
    const was = this.initial.has(id);
    if (on && !was) return 'add';
    if (!on && was) return 'remove';
    return was ? 'member' : null;
  }

  /** The label of the save button: "Save · +2 −1", or "Save" with no change. */
  saveLabel(): string {
    const { add, remove } = this.changes();
    if (!add.length && !remove.length) return 'Save';
    const parts = [add.length ? `+${add.length}` : '', remove.length ? `−${remove.length}` : ''].filter(Boolean);
    return `Save · ${parts.join(' ')}`;
  }
}

/**
 * PickerView draws a PickerState: a search, a kind filter, and the
 * checklist. inline is the picker inside a creation form, where there is
 * nothing to compare with, so rows carry no "will be added".
 */
export class PickerView extends Component {
  constructor(
    readonly state: PickerState,
    private readonly opts: { kinds: boolean; inline: boolean; placeholder: string; onChange?: () => void },
  ) {
    super('div', 'flex min-w-0 flex-col gap-2.5');
    this.actions = {
      'pk-toggle': (t) => {
        this.state.toggle(t.dataset['v'] ?? '');
        this.drawList();
      },
      'pk-kind': (t) => {
        this.state.kind = (t.dataset['v'] ?? 'all') as KindFilter;
        this.render();
      },
    };
    this.root.addEventListener('input', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.classList.contains('pk-q')) {
        this.state.query = t.value;
        this.drawList();
      }
    });
    this.root.addEventListener('keydown', (e) => {
      const t = e.target as HTMLElement;
      if (t.classList.contains('pk-q') && e.key === 'Enter') {
        e.preventDefault();
        const first = this.state.first();
        if (first) {
          this.state.toggle(first.id);
          this.drawList();
        }
      }
    });
  }

  protected view(): Html {
    const kinds: [KindFilter, string][] = [['all', 'All'], ['person', 'People'], ['agent', 'Agents'], ['device', 'Devices']];
    const seg = this.opts.kinds
      ? html`<div class="${ui.seg}" role="group" aria-label="Show">${kinds.map(
          ([k, l]) => html`<button type="button" class="${ui.segBtn}" aria-pressed="${String(this.state.kind === k)}" data-act="pk-kind" data-v="${k}">${l}</button>`,
        )}</div>`
      : Html.empty;
    return html`<div class="flex flex-wrap items-center gap-2"><label class="${ui.search} flex-[1_1_14rem] border border-outline-variant">${Icon.of('search')}<input class="pk-q" type="search" placeholder="${this.opts.placeholder}" aria-label="${this.opts.placeholder}" value="${this.state.query}" autocomplete="off"></label>${seg}</div>
      <div class="pk-list max-h-[min(360px,46vh)] overflow-y-auto rounded-md3-m border border-outline-variant bg-surface-lowest" role="listbox" aria-multiselectable="true"></div><div class="pk-sum min-h-[18px] text-[12.5px] text-on-surface-variant [&_b]:text-on-surface" aria-live="polite"></div>`;
  }

  protected override afterRender(): void {
    this.drawList();
  }

  focus(): void {
    this.find<HTMLInputElement>('.pk-q')?.focus();
  }

  /** Draws the list and the summary only, so the search keeps its focus. */
  private drawList(): void {
    const s = this.state;
    const rows = s.visible();
    const list = this.$('.pk-list');
    list.innerHTML = Html.join(
      rows.map((it) => {
        const on = s.selected.has(it.id);
        const tag = this.opts.inline ? null : s.tag(it.id);
        const pill =
          tag === 'add' ? html`<span class="${ui.pill.ok}">will be added</span>`
          : tag === 'remove' ? html`<span class="${ui.pill.err}">will be removed</span>`
          : tag === 'member' ? html`<span class="${ui.pill.muted}">member</span>`
          : Html.empty;
        return html`<button type="button" class="group flex w-full items-center gap-2.5 border-b border-outline-variant px-3 py-2 text-left last:border-0 hover:bg-surface-low disabled:cursor-default disabled:opacity-50" role="option" aria-selected="${String(on)}" data-act="pk-toggle" data-v="${it.id}" ${Html.raw(it.disabled ? 'disabled' : '')}><span class="grid size-5 shrink-0 place-items-center rounded-[5px] border-2 border-outline group-aria-selected:border-primary group-aria-selected:bg-primary group-aria-selected:text-on-primary [&_.ic]:size-3.5 [&_.ic]:stroke-[2.8]">${on ? Icon.of('check') : ''}</span>${it.avatar}<span class="min-w-0 flex-1"><b class="mr-1.5 font-semibold">${it.label}</b>${it.kind && it.kind !== 'person' ? html`<span class="${ui.kind[it.kind] ?? ''}">${it.kind}</span>` : ''}<span class="block truncate text-[12.5px] text-on-surface-variant">${it.sub}</span></span>${pill}</button>`;
      }),
    ).value || html`<div class="${ui.empty}">Nothing matches “${s.query}”.</div>`.value;
    const { add, remove } = s.changes();
    const shown = rows.length < s.items.length ? `Showing ${rows.length} of ${s.items.length}. ` : '';
    const summary = this.opts.inline
      ? html`<b>${s.selected.size} selected.</b>`
      : add.length || remove.length
        ? html`<b>${add.length} to add, ${remove.length} to remove.</b>`
        : html`No change yet. Tick to add, untick to remove.`;
    this.$('.pk-sum').innerHTML = html`${shown}${summary}`.value;
    this.opts.onChange?.();
  }
}
