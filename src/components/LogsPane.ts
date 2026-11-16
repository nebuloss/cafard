import { Dom } from '@/core/Dom.ts';
import { isNumber, StorageKey, type KeyValueStore } from '@/core/KeyValueStore.ts';
import type { Entry, LogRequest } from '@/model/types.ts';
import { entriesQuery, type CafardApi } from '@/net/CafardApi.ts';
import { ApiError } from '@/net/ApiError.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { Component } from '@/components/Component.ts';
import { ElementScroller } from '@/components/ElementScroller.ts';
import { LogView, type Scroller } from '@/components/LogView.ts';
import type { FrameScheduler } from '@/core/FrameScheduler.ts';
import { logLabel, type PendingLog } from '@/components/AttachmentTray.ts';

/** The logs pane's width, remembered across pages (PLAN 9.6.3). */
export const PANE_WIDTH = new StorageKey<number>('cafard.logsPaneWidth', isNumber);

export type Selection =
  | { readonly mode: 'none' }
  | { readonly mode: 'picking'; readonly from: number }
  | { readonly mode: 'range'; readonly from: number; readonly to: number };

/** The filters a logs view shows, carried into what it attaches. */
export interface LogFilters {
  readonly channel?: string;
  readonly sev?: number;
  readonly re?: string;
}

export function filtersOf(query: string): LogFilters {
  const p = new URLSearchParams(query);
  const sev = p.get('sev');
  const re = p.get('re');
  const channel = p.get('channel');
  return {
    ...(channel === null || channel === '' ? {} : { channel }),
    ...(sev === null || sev === '' || Number.isNaN(Number(sev)) ? {} : { sev: Number(sev) }),
    ...(re === null || re === '' ? {} : { re }),
  };
}

/**
 * The log request of a selected range: one stream when every selected line
 * is from it, else the channel, whose streams the server freezes at send
 * time. The filters on screen go with it, so the agent reads what was seen.
 */
export function rangeRequest(from: number, to: number, streams: readonly string[], f: LogFilters): LogRequest {
  const one = new Set(streams).size === 1 ? streams[0] : undefined;
  return {
    ...(one !== undefined || f.channel === undefined ? { stream: one ?? streams[0] ?? '' } : { channel: f.channel }),
    from_seq: Math.min(from, to),
    to_seq: Math.max(from, to),
    ...(f.re === undefined ? {} : { grep: f.re }),
    ...(f.sev === undefined ? {} : { min_severity: f.sev }),
  };
}

/** How long a touch must rest on a line to start a range (PLAN 9.6.4). */
export const LONG_PRESS_MS = 500;

/** Where a pane scrolls and paints; the chat's own pane by default. */
export interface LogsPaneOptions {
  /** The window on /live; the pane's list on a chat page. */
  readonly scroller?: Scroller;
  readonly frames?: FrameScheduler;
}

export interface LogsPaneHooks {
  attach(log: PendingLog): void;
  copy(text: string): void;
  toast(text: string, tone?: 'info' | 'error'): void;
  prompt(question: string): string | null;
}

/**
 * The logs of a bound channel (PLAN 9.6.3, 9.6.4): a LogView of its
 * streams merged, following live; line actions; range selection with a
 * bar to attach the range to the chat's composer; a resizable width.
 */
export class LogsPane extends Component {
  log!: LogView;
  private list!: HTMLElement;
  private selection: Selection = { mode: 'none' };
  private menuSeq = 0;
  private readonly filters: LogFilters;

  constructor(
    root: HTMLElement,
    private readonly renderer: EntryRenderer,
    private readonly api: CafardApi,
    private readonly storage: KeyValueStore,
    private readonly layout: HTMLElement | null,
    private readonly doc: Document,
    private readonly hooks: LogsPaneHooks,
    private readonly options: LogsPaneOptions = {},
  ) {
    super(root);
    this.filters = filtersOf(Dom.find('[data-component="LogView"]', root)?.dataset['query'] ?? '');
  }

  protected override build(): void {
    const mount = Dom.find('[data-component="LogView"]', this.root) ?? this.root;
    this.list = Dom.find('[data-slot="list"]', mount) ?? mount;
    this.log = new LogView(mount, this.renderer, this.options.scroller ?? new ElementScroller(this.list), this.doc, LogView.cap, this.options.frames);
  }

  protected bind(): void {
    this.bag.add(this.log.mount());
    const width = this.storage.get(PANE_WIDTH);
    if (width !== null) this.setWidth(width);
    this.bag.listen(this.list, 'click', (event) => this.lineClick(event as MouseEvent));
    this.bag.listen(this.list, 'contextmenu', (event) => {
      const row = (event.target as Element | null)?.closest<HTMLElement>('[data-seq]');
      if (row === null || row === undefined) return;
      event.preventDefault();
      // A phone fires contextmenu on a long press, which starts a range.
      if (this.touching) return;
      this.openMenu(row);
    });
    this.bindLongPress();
    const menu = Dom.find('[data-slot="line-menu"]', this.root);
    if (menu !== null) this.bag.listen(menu, 'click', (event) => this.menuAction(event, menu));
    const bar = Dom.find('[data-component="RangeBar"]', this.root);
    if (bar !== null) this.bag.listen(bar, 'click', (event) => this.barAction(event));
    const handle = Dom.find('[data-slot="resize"]', this.root);
    if (handle !== null) this.bindResize(handle);
  }

  /** Entries from a live feed, painted on the next animation frame. */
  enqueue(entries: readonly Entry[]): void {
    this.log.enqueue(entries);
  }

  /** Shows an event row's entry with 20 lines of context around it. */
  async focus(seq: number): Promise<void> {
    try {
      const page = await this.api.context(seq, 20, 20);
      if (this.log.follow.value.mode === 'following') this.log.toggle();
      this.log.replace(page.items);
      this.highlight(seq, seq);
      Dom.find(`[data-seq="${seq}"]`, this.list)?.scrollIntoView({ block: 'center' });
    } catch (error) {
      this.hooks.toast(error instanceof ApiError ? error.message : 'The lines could not be loaded.', 'error');
    }
  }

  /** Shows a log attachment's range ("Open"), from its /live link. */
  async showRange(href: string): Promise<void> {
    try {
      const page = await this.api.entries({ ...entriesQuery(href.split('?')[1] ?? ''), limit: 1000 });
      if (this.log.follow.value.mode === 'following') this.log.toggle();
      this.log.replace(page.items);
      const first = page.items[0];
      const last = page.items[page.items.length - 1];
      if (first !== undefined && last !== undefined) this.highlight(first.seq, last.seq);
    } catch (error) {
      this.hooks.toast(error instanceof ApiError ? error.message : 'The lines could not be loaded.', 'error');
    }
  }

  private touching = false;
  private pressed = false; // a long press just started a range: its click is not a tap

  /** A long press on a line starts a range, as "Select a range" does. */
  private bindLongPress(): void {
    const win = this.doc.defaultView;
    if (win === null) return;
    let timer: number | undefined;
    let x = 0;
    let y = 0;
    const cancel = (): void => {
      if (timer !== undefined) win.clearTimeout(timer);
      timer = undefined;
    };
    this.bag.defer(cancel);
    this.bag.listen(this.list, 'pointerdown', (event) => {
      const e = event as PointerEvent;
      this.touching = e.pointerType === 'touch';
      if (!this.touching) return;
      const row = (e.target as Element | null)?.closest<HTMLElement>('[data-seq]');
      if (row === null || row === undefined || row.dataset['kind'] === 'message') return;
      x = e.clientX;
      y = e.clientY;
      cancel();
      timer = win.setTimeout(() => {
        timer = undefined;
        this.pressed = true;
        this.select({ mode: 'picking', from: Number(row.dataset['seq']) });
      }, LONG_PRESS_MS);
    });
    this.bag.listen(this.list, 'pointermove', (event) => {
      const e = event as PointerEvent;
      if (timer !== undefined && Math.hypot(e.clientX - x, e.clientY - y) > 10) cancel();
    });
    for (const type of ['pointerup', 'pointercancel', 'pointerleave']) this.bag.listen(this.list, type, cancel);
  }

  private lineClick(event: MouseEvent): void {
    if (this.pressed) {
      this.pressed = false;
      return;
    }
    const row = (event.target as Element | null)?.closest<HTMLElement>('[data-seq]');
    if (row === null || row === undefined) return;
    const seq = Number(row.dataset['seq']);
    const s = this.selection;
    if (s.mode === 'picking' || (event.shiftKey && s.mode !== 'none')) {
      this.select({ mode: 'range', from: s.from, to: seq });
      return;
    }
    if (event.shiftKey) {
      this.select({ mode: 'picking', from: seq });
      return;
    }
    this.openMenu(row);
  }

  private openMenu(row: HTMLElement): void {
    const menu = Dom.find('[data-slot="line-menu"]', this.root);
    if (menu === null) return;
    this.menuSeq = Number(row.dataset['seq']);
    // A message in Live can be copied, not attached or marked.
    const message = row.dataset['kind'] === 'message';
    for (const item of Dom.findAll<HTMLElement>('[data-log-only]', menu)) item.hidden = message;
    row.after(menu);
    menu.hidden = false;
    menu.querySelector('button')?.focus();
  }

  private lineOf(seq: number): HTMLElement | null {
    return Dom.find(`[data-seq="${seq}"]`, this.list);
  }

  private streamOf(seq: number): string {
    const row = this.lineOf(seq);
    if (row === null) return '';
    const tag = Dom.find('[data-slot="stream"]', row);
    if (tag !== null) return tag.textContent ?? '';
    // A marker reads "<stream>: <text>"; stream ids have no spaces.
    const text = Dom.find('[data-slot="text"]', row)?.textContent ?? '';
    const i = text.indexOf(': ');
    return i > 0 ? text.slice(0, i) : '';
  }

  private menuAction(event: Event, menu: HTMLElement): void {
    const action = (event.target as Element | null)?.closest<HTMLElement>('[data-action]')?.dataset['action'];
    if (action === undefined) return;
    menu.hidden = true;
    const seq = this.menuSeq;
    const row = this.lineOf(seq);
    const stream = this.streamOf(seq);
    switch (action) {
      case 'copy-line':
        this.hooks.copy(row?.textContent ?? '');
        break;
      case 'copy-seq':
        this.hooks.copy(String(seq));
        break;
      case 'context':
        void this.focus(seq);
        break;
      case 'mark': {
        const label = this.hooks.prompt(`Marker label for ${stream}`);
        if (label !== null && label.trim() !== '') {
          this.api.mark(stream, label).catch((error: unknown) => this.hooks.toast(error instanceof ApiError ? error.message : 'The marker was not written.', 'error'));
        }
        break;
      }
      case 'attach-line':
        this.attach({ stream, from_seq: seq, to_seq: seq }, 1);
        break;
      case 'attach-from':
        this.attach({ stream, from_seq: seq }, undefined);
        break;
      case 'select':
        this.select({ mode: 'picking', from: seq });
        break;
    }
  }

  private select(s: Selection): void {
    this.selection = s;
    if (s.mode === 'none') this.highlight(0, -1);
    else if (s.mode === 'picking') this.highlight(s.from, s.from);
    else this.highlight(s.from, s.to);
    const bar = Dom.find('[data-component="RangeBar"]', this.root);
    if (bar === null) return;
    bar.hidden = s.mode === 'none';
    const n = this.selected().length;
    Dom.setText(Dom.find('[data-slot="count"]', bar), s.mode === 'picking' ? 'Tap the last line' : `${n} ${n === 1 ? 'line' : 'lines'}`);
  }

  private highlight(from: number, to: number): void {
    const lo = Math.min(from, to);
    const hi = Math.max(from, to);
    for (const row of Dom.findAll('[data-seq]', this.list)) {
      const seq = Number(row.dataset['seq']);
      if (seq >= lo && seq <= hi) row.setAttribute('data-selected', '');
      else row.removeAttribute('data-selected');
    }
  }

  private selected(): HTMLElement[] {
    return Dom.findAll('.line[data-selected]:not([data-kind="message"])', this.list);
  }

  private barAction(event: Event): void {
    const action = (event.target as Element | null)?.closest<HTMLElement>('[data-action]')?.dataset['action'];
    const s = this.selection;
    if (action === 'cancel' || s.mode !== 'range') {
      if (action === 'cancel') this.select({ mode: 'none' });
      return;
    }
    const lines = this.selected();
    if (action === 'copy') this.hooks.copy(lines.map((l) => l.textContent ?? '').join('\n'));
    if (action === 'attach') {
      const streams = lines.map((l) => Dom.find('[data-slot="stream"]', l)?.textContent ?? '');
      if (new Set(streams).size > 1 && this.filters.channel === undefined) {
        // An attachment is one stream or one channel's streams.
        this.hooks.toast(`The range spans several streams; only the lines of ${streams[0] ?? ''} are attached. Pick a channel to attach them all.`);
      }
      this.attach(rangeRequest(s.from, s.to, streams, this.filters), lines.length);
      this.select({ mode: 'none' });
    }
  }

  private attach(request: LogRequest, lines: number | undefined): void {
    const full: LogRequest = {
      ...request,
      ...(this.filters.re === undefined || request.grep !== undefined ? {} : { grep: this.filters.re }),
      ...(this.filters.sev === undefined || request.min_severity !== undefined ? {} : { min_severity: this.filters.sev }),
    };
    this.hooks.attach({ request: full, label: logLabel(full, lines) });
  }

  private setWidth(px: number): void {
    const w = Math.round(Math.min(900, Math.max(280, px)));
    this.layout?.style.setProperty('--pane-logs', `${w}px`);
  }

  /** Dragging the pane's edge sets its width, kept in storage. */
  private bindResize(handle: HTMLElement): void {
    let dragging = false;
    this.bag.listen(handle, 'pointerdown', (event) => {
      dragging = true;
      handle.setPointerCapture?.((event as PointerEvent).pointerId);
    });
    this.bag.listen(handle, 'pointermove', (event) => {
      if (!dragging) return;
      const right = this.root.getBoundingClientRect().right;
      this.setWidth(right - (event as PointerEvent).clientX);
    });
    this.bag.listen(handle, 'pointerup', () => {
      if (!dragging) return;
      dragging = false;
      this.storage.set(PANE_WIDTH, Math.round(this.root.getBoundingClientRect().width));
    });
    this.bag.listen(handle, 'keydown', (event) => {
      const key = (event as KeyboardEvent).key;
      if (key !== 'ArrowLeft' && key !== 'ArrowRight') return;
      const w = this.root.getBoundingClientRect().width + (key === 'ArrowLeft' ? 24 : -24);
      this.setWidth(w);
      this.storage.set(PANE_WIDTH, Math.round(w));
    });
  }
}
