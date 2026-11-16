import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import { AnimationFrames, type FrameScheduler } from '@/core/FrameScheduler.ts';
import { AttachmentTray } from '@/components/AttachmentTray.ts';
import { FilterBar } from '@/components/FilterBar.ts';
import { LogsPane } from '@/components/LogsPane.ts';
import { WindowScroller } from '@/components/LogView.ts';
import { DropForm, QuickDropSheet } from '@/components/QuickDropSheet.ts';
import { UploadStatus } from '@/components/UploadStatus.ts';
import type { EntriesQuery } from '@/net/CafardApi.ts';
import { ApiError } from '@/net/ApiError.ts';
import { chatLabel } from '@/render/format.ts';
import { RendererRegistry } from '@/render/RendererRegistry.ts';
import { ATTACHMENT_CHIP_ATTRIBUTES, UPLOAD_STATUS_ATTRIBUTES } from '@/render/renderers/AttachmentChipRenderer.ts';
import { LINE_ATTRIBUTES, LineRenderer, MessageLineRenderer } from '@/render/renderers/LineRenderer.ts';
import { MARKER_ATTRIBUTES, MarkerRenderer } from '@/render/renderers/MarkerRenderer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell } from '@/pages/Shell.ts';
import { copier } from '@/pages/chatParts.ts';

/** What a reload after a reset shows, like the server-rendered page. */
const RELOAD_LINES = 500;

/**
 * /live (PLAN 9.6.4): the filters in the URL (a sheet on compact
 * screens), the lines following live at the pace of animation frames,
 * markers as dividers, messages with kinds=message, and the line actions
 * and range selection of the logs pane. Attaching opens the quick drop
 * sheet with the log chip and a chat picker.
 */
export class LivePage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
    private readonly frames: FrameScheduler = new AnimationFrames(doc.defaultView),
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const { doc, services } = this;
    const win = doc.defaultView;
    const root = Dom.find('[data-component="LivePane"]', doc);
    const mount = Dom.find('[data-component="LogView"]', doc);
    const filter = Dom.find<HTMLDetailsElement>('[data-component="FilterBar"]', doc);
    if (filter !== null) bag.add(new FilterBar(filter, win?.matchMedia?.('(min-width: 600px)') ?? null, doc).mount());
    const line = SlotTemplate.find(doc, 'tpl-line', LINE_ATTRIBUTES);
    const marker = SlotTemplate.find(doc, 'tpl-marker', MARKER_ATTRIBUTES);
    const query = mount?.dataset['query'] ?? '';
    const follow = mount?.dataset['follow'] === 'true';
    const shell = mountShell(doc, services, bag, follow ? (query === '' ? '/api/events' : `/api/events?${query}`) : '/api/events?scope=status');
    if (root === null || mount === null || line === null || marker === null || win === null) {
      bag.add(shell.feed.start());
      return bag;
    }
    const toast = (text: string, tone: 'info' | 'error' = 'info'): void => void shell.toast?.show(text, tone);

    const registry = new RendererRegistry(new LineRenderer(line))
      .register('marker', new MarkerRenderer(marker))
      .register('message', new MessageLineRenderer(line));
    const drop = this.quickDrop(bag, toast);
    const pane = new LogsPane(
      root,
      registry,
      services.api,
      services.storage,
      null,
      doc,
      {
        attach: (log) => {
          if (drop === null) {
            toast('Attaching lines to a message needs the message scope.', 'error');
            return;
          }
          drop.tray.addLog(log);
          drop.sheet.open();
        },
        copy: copier(win),
        toast,
        prompt: (q) => win.prompt(q),
      },
      { scroller: new WindowScroller(win), frames: this.frames },
    );
    bag.add(pane.mount());

    bag.add(shell.feed.events.on('entry', (entry) => pane.enqueue([entry])));
    bag.add(
      shell.feed.events.on('reset', () => {
        void this.reload(query, pane)
          .then((next) => shell.feed.restart(next))
          .catch((error: unknown) => toast(error instanceof ApiError ? error.message : 'Reloading the lines failed.', 'error'));
      }),
    );
    const next = Dom.number(mount, 'data-next-seq') ?? 0;
    bag.add(shell.feed.start(follow ? next : 0));
    return bag;
  }

  /** The quick drop sheet a range is attached through, when the page has it. */
  private quickDrop(bag: DisposableBag, toast: (t: string, tone?: 'info' | 'error') => void): { sheet: QuickDropSheet; tray: AttachmentTray } | null {
    const { doc, services } = this;
    const sheetEl = Dom.find<HTMLDialogElement>('[data-component="QuickDropSheet"]', doc);
    const form = sheetEl === null ? null : Dom.find<HTMLFormElement>('form.drop-form', sheetEl);
    if (sheetEl === null || form === null) return null;
    const sheet = new QuickDropSheet(sheetEl);
    bag.add(sheet.mount());
    const chip = SlotTemplate.find(doc, 'tpl-attachment-chip', ATTACHMENT_CHIP_ATTRIBUTES);
    const tray = new AttachmentTray(Dom.find('[data-slot="tray"]', form) ?? doc.createElement('div'), chip, Number(form.dataset['maxFiles'] ?? '10'));
    bag.add(tray.mount());
    const uploads = Dom.find('[data-slot="uploads"]', form);
    const upload = SlotTemplate.find(doc, 'tpl-upload-status', UPLOAD_STATUS_ATTRIBUTES);
    if (uploads !== null) bag.add(new UploadStatus(uploads, upload, services.uploads).mount());
    bag.add(new DropForm(form, tray, services.uploads, services.storage, services.clock, () => sheet.close()).mount());
    bag.add(services.uploads.events.on('sent', ({ entry }) => toast(`Sent to ${chatLabel(entry.chat ?? '')}.`)));
    void services.uploads.restore();
    return { sheet, tray };
  }

  /** Reloads the latest lines after a reset; returns where to follow from. */
  private async reload(query: string, pane: LogsPane): Promise<number> {
    const page = await this.services.api.entries({ ...entriesQuery(query), limit: RELOAD_LINES });
    pane.log.replace(page.items);
    return page.next_seq ?? pane.log.cursor + 1;
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
