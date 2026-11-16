import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import type { Stream } from '@/model/types.ts';
import { activity, rate } from '@/render/format.ts';
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
   * Applies a status change. The event carries what status knows (state,
   * rate, drops, line state); the last line stays as rendered.
   */
  update(s: Stream): HTMLElement {
    let card = Dom.findAll('[data-stream]', this.rows).find((el) => el.dataset['stream'] === s.id) ?? null;
    if (card === null) {
      const filled = this.template.clone().attr({ 'data-stream': s.id });
      filled.text('name', s.id).text('preview', s.last_line ?? '');
      filled.slotAttr('open', { href: `/live?${new URLSearchParams({ streams: s.id }).toString()}` });
      card = filled.root;
      const after = Dom.findAll('[data-stream]', this.rows).find((el) => (el.dataset['stream'] ?? '') > s.id);
      this.rows.insertBefore(card, after ?? null);
    }
    card.dataset['status'] = s.status;
    const set = (slot: string, text: string): void => Dom.setText(Dom.find(`[data-slot="${slot}"]`, card), text);
    set('status', s.status);
    set('transport', `${s.kind} / ${s.transport}`);
    set('rate', rate(s.rate_per_min));
    set('last', activity(s.last_activity, this.now()));
    set('dropped', String(s.dropped));
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
}
