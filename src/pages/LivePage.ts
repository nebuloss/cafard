import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import { FilterBar } from '@/components/FilterBar.ts';
import { LogView, WindowScroller } from '@/components/LogView.ts';
import type { Entry } from '@/model/types.ts';
import type { EntriesQuery } from '@/net/CafardApi.ts';
import { ApiError } from '@/net/ApiError.ts';
import { RendererRegistry } from '@/render/RendererRegistry.ts';
import { LINE_ATTRIBUTES, LineRenderer } from '@/render/renderers/LineRenderer.ts';
import { MARKER_ATTRIBUTES, MarkerRenderer } from '@/render/renderers/MarkerRenderer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell } from '@/pages/Shell.ts';

/** How long arriving lines are gathered before they reach the DOM. */
const BATCH_MS = 50;
/** What a reload after a reset shows, like the server-rendered page. */
const RELOAD_LINES = 500;

/**
 * /live: the LogView mount (M3). The stream picker and line actions come
 * with the full page in M5. The server rendered the range or the last 500
 * lines; this follows from data-next-seq.
 */
export class LivePage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const win = this.doc.defaultView;
    const mount = Dom.find('[data-component="LogView"]', this.doc);
    const filter = Dom.find<HTMLDetailsElement>('[data-component="FilterBar"]', this.doc);
    if (filter !== null) bag.add(new FilterBar(filter, win?.matchMedia?.('(min-width: 600px)') ?? null, this.doc).mount());
    const line = SlotTemplate.find(this.doc, 'tpl-line', LINE_ATTRIBUTES);
    const marker = SlotTemplate.find(this.doc, 'tpl-marker', MARKER_ATTRIBUTES);
    const query = mount?.dataset['query'] ?? '';
    const follow = mount?.dataset['follow'] === 'true';
    const shell = mountShell(this.doc, this.services, bag, follow ? (query === '' ? '/api/events' : `/api/events?${query}`) : '/api/events?scope=status');
    if (mount === null || line === null || marker === null || win === null) {
      bag.add(shell.feed.start());
      return bag;
    }

    const registry = new RendererRegistry(new LineRenderer(line)).register('marker', new MarkerRenderer(marker));
    const log = new LogView(mount, registry, new WindowScroller(win), this.doc);
    bag.add(log.mount());

    let pending: Entry[] = [];
    let timer: Disposable | null = null;
    bag.defer(() => timer?.dispose());
    const flush = (): void => {
      timer = null;
      const batch = pending;
      pending = [];
      log.append(batch);
    };
    bag.add(
      shell.feed.events.on('entry', (entry) => {
        if (entry.kind === 'message') return; // messages in Live come with track D's renderers
        pending.push(entry);
        timer ??= this.services.clock.setTimeout(flush, BATCH_MS);
      }),
    );
    bag.add(
      shell.feed.events.on('reset', () => {
        void this.reload(query, log)
          .then((next) => shell.feed.restart(next))
          .catch((error: unknown) => {
            shell.toast?.show(error instanceof ApiError ? error.message : 'Reloading the lines failed.', 'error');
          });
      }),
    );
    const next = Dom.number(mount, 'data-next-seq') ?? 0;
    bag.add(shell.feed.start(follow ? next : 0));
    return bag;
  }

  /** Reloads the latest lines after a reset; returns where to follow from. */
  private async reload(query: string, log: LogView): Promise<number> {
    const page = await this.services.api.entries({ ...entriesQuery(query), limit: RELOAD_LINES });
    log.replace(page.items);
    return page.next_seq ?? log.cursor + 1;
  }
}

/** The /live query as an /api/entries query. */
export function entriesQuery(query: string): EntriesQuery {
  const p = new URLSearchParams(query);
  const get = (k: string): string | undefined => p.get(k) ?? undefined;
  const num = (k: string): number | undefined => {
    const v = p.get(k);
    return v === null || v === '' || Number.isNaN(Number(v)) ? undefined : Number(v);
  };
  return {
    streams: get('streams'),
    channel: get('channel'),
    sev: get('sev'),
    re: get('re'),
    kinds: get('kinds'),
    from_seq: num('from_seq'),
    to_seq: num('to_seq'),
  };
}
