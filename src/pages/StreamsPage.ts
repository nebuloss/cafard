import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom, h } from '@/core/Dom.ts';
import type { Stream } from '@/model/types.ts';
import { activity, claimText, rate } from '@/render/format.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell } from '@/pages/Shell.ts';

export const STREAM_CARD_ATTRIBUTES = ['data-stream', 'data-status', 'href'] as const;

/**
 * /streams: keeps the server-rendered sources current from stream_status
 * events, and adds new ones from tpl-stream-card.
 */
export class StreamsPage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const shell = mountShell(this.doc, this.services, bag, '/api/events?scope=status');
    const table = Dom.find('[data-component="StreamList"]', this.doc);
    const rows = table === null ? null : Dom.find('[data-slot="rows"]', table);
    const template = SlotTemplate.find(this.doc, 'tpl-stream-card', STREAM_CARD_ATTRIBUTES);
    if (table !== null && rows !== null && template !== null) {
      const cards = new StreamCards(rows, template, () => this.services.clock.now());
      bag.add(
        shell.feed.events.on('stream_status', (s) => {
          cards.update(s);
          table.hidden = false;
        }),
      );
    }
    bag.add(shell.feed.start());
    return bag;
  }
}

/** The rows of the sources table, by stream id. */
export class StreamCards {
  constructor(
    private readonly rows: HTMLElement,
    private readonly template: SlotTemplate,
    private readonly now: () => number,
  ) {}

  /**
   * Applies a status change. The event carries the state, rate, drops and
   * line state, and (since track G) the last line and the claim holder: no
   * claim in an event means nobody holds the stream.
   */
  update(s: Stream): HTMLElement {
    let card = Dom.findAll('[data-stream]', this.rows).find((el) => el.dataset['stream'] === s.id) ?? null;
    if (card === null) {
      const filled = this.template.clone().attr({ 'data-stream': s.id });
      filled.text('name', s.id).text('preview', s.last_line ?? '');
      filled.slotAttr('open', { href: `/live?${new URLSearchParams({ streams: s.id }).toString()}` });
      card = filled.root;
      const group = this.group(s.project ?? '');
      const after = Dom.findAll('[data-stream]', group).find((el) => (el.dataset['stream'] ?? '') > s.id);
      group.insertBefore(card, after ?? null);
    }
    card.dataset['status'] = s.status;
    const set = (slot: string, text: string): void => Dom.setText(Dom.find(`[data-slot="${slot}"]`, card), text);
    set('status', s.status);
    set('transport', `${s.kind} / ${s.transport}`);
    set('rate', rate(s.rate_per_min));
    set('last', activity(s.last_activity, this.now()));
    set('dropped', String(s.dropped));
    if (s.last_line !== undefined) set('preview', s.last_line);
    const claim = Dom.find('[data-slot="claim"]', card);
    if (claim !== null) claim.replaceChildren(...(s.claim === undefined ? [] : [h('span', { class: 'chip' }, claimText(s.claim))]));
    if (s.line_state !== undefined) {
      const ls = s.line_state;
      const parts: string[] = [];
      for (const [name, v] of [['DTR', ls.dtr], ['RTS', ls.rts], ['CTS', ls.cts], ['DCD', ls.dcd]] as const) {
        if (v !== undefined) parts.push(`${name} ${v ? 'on' : 'off'}`);
      }
      set('line-state', parts.join(', '));
    }
    return card;
  }

  /** The rows of a project, made (titled with its name) when it has none yet. */
  private group(project: string): HTMLElement {
    const groups = Dom.findAll('.stream-group', this.rows);
    const found = groups.find((el) => (el.dataset['project'] ?? '') === project);
    if (found !== undefined) return found;
    const group = h('div', { class: 'stream-group', role: 'rowgroup', 'data-project': project },
      h('div', { class: 'stream-group-head', role: 'row' }, h('span', { class: 'list-heading', role: 'rowheader' }, project === '' ? 'Outside projects' : project)));
    const after = groups.find((el) => project !== '' && ((el.dataset['project'] ?? '') === '' || (el.dataset['project'] ?? '') > project));
    this.rows.insertBefore(group, after ?? null);
    return group;
  }
}
