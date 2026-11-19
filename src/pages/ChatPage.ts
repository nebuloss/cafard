import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import { AnimationFrames, type FrameScheduler } from '@/core/FrameScheduler.ts';
import type { Chat, Entry } from '@/model/types.ts';
import { ApiError } from '@/net/ApiError.ts';
import { linesText } from '@/render/format.ts';
import { AttachmentTray, logLabel, type PendingLog } from '@/components/AttachmentTray.ts';
import { ChannelDialog } from '@/components/ChannelDialog.ts';
import { ChannelHeader } from '@/components/ChannelHeader.ts';
import { Composer } from '@/components/Composer.ts';
import { ConversationView } from '@/components/ConversationView.ts';
import { ElementScroller } from '@/components/ElementScroller.ts';
import { GalleryPicker } from '@/components/GalleryPicker.ts';
import { ImageViewer } from '@/components/ImageViewer.ts';
import { LogsPane } from '@/components/LogsPane.ts';
import { QuestionCard } from '@/components/QuestionCard.ts';
import { ReadTracker } from '@/components/ReadTracker.ts';
import { UploadStatus } from '@/components/UploadStatus.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell } from '@/pages/Shell.ts';
import { chatRenderers, copier, viewerOf } from '@/pages/chatParts.ts';

/** Where a feed entry goes on a chat page (CONTRACTS.md 4, chat scope). */
export interface Routing {
  readonly conversation: boolean;
  readonly logs: boolean;
  readonly event: boolean;
}

/**
 * The chat's own messages and markers go to the conversation; the bound
 * streams' lines and markers to the logs pane, and, per the channel's
 * surface, markers (and error lines) also as event rows.
 */
export function route(e: Entry, chat: string, stream: string, surface: string): Routing {
  if (e.kind === 'message') return { conversation: e.chat === chat, logs: false, event: false };
  if (e.stream === stream) return { conversation: true, logs: false, event: false };
  const event = surface !== 'none' && (e.kind === 'marker' || (surface === 'errors' && e.severity <= 3));
  return { conversation: event, logs: true, event };
}

/** The stream id of a chat id, for the chat's own markers. */
export function streamOf(chat: string, viewer: string): string {
  if (!chat.startsWith('dm:')) return chat;
  const peer = chat.slice(3);
  return viewer < peer ? `dm:${viewer}~${peer}` : `dm:${peer}~${viewer}`;
}

/**
 * A conversation (PLAN 9.6.3): the timeline, the composer, the logs pane
 * of a bound channel, the header and settings, all on one feed of the
 * chat's messages and its streams' entries.
 */
export class ChatPage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
    private readonly frames: FrameScheduler = new AnimationFrames(doc.defaultView),
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const { doc, services } = this;
    const win = doc.defaultView;
    const layout = Dom.find('.chat-layout', doc);
    const chat = layout?.dataset['chat'] ?? '';
    const surface = layout?.dataset['surface'] ?? 'none';
    const viewer = viewerOf(layout);
    const stream = streamOf(chat, viewer.name);
    const shell = mountShell(doc, services, bag, `/api/events?chat=${encodeURIComponent(chat)}`);
    const toast = shell.notify;
    const copy = copier(win);
    const r = chatRenderers(doc, viewer, services.clock);
    const queue = services.uploads;
    const go = (path: string): void => win?.location.assign(path);

    // Logs pane.
    const paneEl = Dom.find('[data-component="LogsPane"]', doc);
    let pane: LogsPane | null = null;
    let composer: Composer | null = null;
    if (paneEl !== null && r.logs !== null) {
      pane = new LogsPane(
        paneEl,
        r.logs,
        services.api,
        services.storage,
        layout,
        doc,
        {
          attach: (log) => composer?.addLog(log),
          copy,
          toast,
          prompt: (q) => win?.prompt(q) ?? null,
        },
        { frames: this.frames },
      );
      bag.add(pane.mount());
    }
    const paneShown = (): boolean => paneEl !== null && paneEl.offsetParent !== null;

    // Conversation.
    const convEl = Dom.find('[data-component="ConversationView"]', doc);
    const scrollEl = convEl?.closest<HTMLElement>('.pane-conv') ?? convEl;
    const viewerEl = Dom.find<HTMLDialogElement>('[data-component="ImageViewer"]', doc);
    const images = viewerEl === null ? null : new ImageViewer(viewerEl);
    if (images !== null) bag.add(images.mount());
    const reads = win !== null && typeof win.IntersectionObserver === 'function'
      ? bag.add(new ReadTracker((cb) => new win.IntersectionObserver((entries) => cb(entries), { threshold: 0.6 }), services.clock, (seqs) => services.api.ack(seqs)))
      : null;
    let conversation: ConversationView | null = null;
    if (convEl !== null && scrollEl !== null) {
      conversation = new ConversationView(convEl, viewer.name, convEl.dataset['filter'] ?? '', r.timeline, new ElementScroller(scrollEl), reads, {
        reply: (seq, quote) => composer?.setReply(seq, quote),
        viewImages: (list, index) => images?.open(list, index),
        copy,
        openLog: (href) => {
          if (pane === null || !paneShown()) return false;
          void pane.showRange(href);
          return true;
        },
        openEvent: (seq) => {
          if (pane === null || !paneShown()) return false;
          void pane.focus(seq);
          return true;
        },
        older: (before) => services.api.timeline(chat, { before_seq: before, ...(convEl.dataset['filter'] ? { f: convEl.dataset['filter'] } : {}) }),
      }, Dom.find<HTMLButtonElement>('[data-slot="new"]', scrollEl));
      bag.add(conversation.mount());
    }
    const cardsRoot = Dom.find('main', doc);
    const cards = cardsRoot === null ? null : new QuestionCard(cardsRoot, queue, services.clock);
    if (cards !== null) bag.add(cards.mount());

    // Composer.
    const form = Dom.find<HTMLFormElement>('form.composer', doc);
    if (form !== null) {
      const tray = new AttachmentTray(Dom.find('[data-slot="tray"]', form) ?? doc.createElement('div'), r.chip, Number(form.dataset['maxFiles'] ?? '10'));
      bag.add(tray.mount());
      const pickerEl = Dom.find<HTMLDialogElement>('[data-component="GalleryPicker"]', form);
      const picker = pickerEl === null ? null : new GalleryPicker(pickerEl, services.api, (ref) => composer?.insertRef(ref));
      if (picker !== null) bag.add(picker.mount());
      composer = new Composer(form, chat, tray, queue, services.clock, doc, (s, mode) => this.attachFrom(s, mode, toast), picker === null ? null : () => picker.open());
      bag.add(composer.mount());
      const uploads = Dom.find('[data-slot="uploads"]', form);
      if (uploads !== null) bag.add(new UploadStatus(uploads, r.upload, queue, chat).mount());
    }
    bag.add(queue.events.on('sent', ({ entry }) => conversation?.add(entry)));

    // Header and settings.
    let current: Chat | null = null;
    const dialogEl = Dom.find<HTMLDialogElement>('[data-component="ChannelDialog"]', doc);
    const dialog = dialogEl === null ? null : new ChannelDialog(dialogEl, dialogEl.dataset['channel'] ?? '', services.api, services.clock, go, (q) => win?.confirm(q) === true, (c) => onChat(c));
    if (dialog !== null) bag.add(dialog.mount());
    const openSettings = (): void => {
      if (dialog === null) return;
      if (current !== null) {
        dialog.open(current);
        return;
      }
      services.api
        .chats({ all: true, archived: true })
        .then((chats) => {
          current = chats.find((c) => c.id === chat) ?? null;
          if (current !== null) dialog.open(current);
        })
        .catch((e: unknown) => toast(e instanceof ApiError ? e.message : 'The settings could not be loaded.', 'error'));
    };
    const headerEl = Dom.find('[data-component="ChannelHeader"]', doc);
    const header = headerEl === null ? null : new ChannelHeader(headerEl, openSettings);
    if (header !== null) bag.add(header.mount());
    const onChat = (c: Chat): void => {
      if (c.id !== chat) return;
      const rebind = current !== null && current.patterns.join() !== c.patterns.join();
      current = c;
      header?.update(c);
      if (rebind) shell.feed.restart(shell.feed.cursor);
    };
    bag.add(shell.feed.events.on('chat', onChat));
    bag.add(shell.feed.events.on('stream_status', (s) => header?.status(s)));
    if (win?.location.hash === '#settings') openSettings();

    // The feed.
    const following = convEl?.dataset['follow'] === 'true';
    bag.add(
      shell.feed.events.on('entry', (e) => {
        const where = route(e, chat, stream, surface);
        // An older page ("Older") stays as it was; the latest one follows.
        if (where.conversation && following) {
          const row = conversation?.add(where.event ? { ...e, event: true } : e);
          if (row !== null && row !== undefined && e.question?.open === true && e.sender?.name !== viewer.name) row.focus({ preventScroll: true });
        }
        if (where.logs) {
          pane?.enqueue([e]);
          if (e.severity <= 3 && layout?.dataset['view'] !== 'logs') header?.errorDot(true);
        }
      }),
    );
    bag.add(shell.feed.events.on('seen', (s) => conversation?.markSeen(s)));
    bag.add(shell.feed.events.on('answered', (a) => cards?.answered(a)));
    bag.add(
      shell.feed.events.on('reset', () => {
        void this.reload(chat, conversation, pane)
          .then((next) => shell.feed.restart(next))
          .catch((e: unknown) => toast(e instanceof ApiError ? e.message : 'Reloading the chat failed.', 'error'));
      }),
    );

    void queue.restore();
    if (win !== null) bag.listen(win, 'online', () => queue.kick());
    const next = convEl === null ? 0 : (Dom.number(convEl, 'data-next-seq') ?? 0);
    bag.add(shell.feed.start(following ? next : 0));
    return bag;
  }

  /** After a reset: the latest page again; returns where to follow from. */
  private async reload(chat: string, conversation: ConversationView | null, pane: LogsPane | null): Promise<number> {
    const page = await this.services.api.timeline(chat);
    conversation?.replace(page.items);
    if (pane !== null && chat.startsWith('chan:')) {
      const logs = await this.services.api.entries({ channel: chat.slice(5), limit: 200 });
      pane.log.replace(logs.items);
    }
    return page.next_seq ?? 0;
  }

  /** The "+" menu: the last 200 lines of a stream, or the stream from now on. */
  private async attachFrom(stream: string, mode: 'last' | 'from-now', toast: (t: string, tone?: 'info' | 'error') => void): Promise<PendingLog | null> {
    try {
      const page = await this.services.api.entries({ streams: stream, limit: mode === 'last' ? 200 : 1 });
      const first = page.items[0];
      const last = page.items[page.items.length - 1];
      if (mode === 'from-now') {
        const request = { stream, from_seq: (last?.seq ?? page.next_seq ?? 1) + (last === undefined ? 0 : 1) };
        return { request, label: logLabel(request) };
      }
      if (first === undefined || last === undefined) {
        toast(`${stream} has no lines yet.`);
        return null;
      }
      const request = { stream, from_seq: first.seq, to_seq: last.seq, title: `last ${linesText(page.items.length, false)}` };
      return { request, label: logLabel(request, page.items.length) };
    } catch (e) {
      toast(e instanceof ApiError ? e.message : 'The lines could not be read.', 'error');
      return null;
    }
  }
}
