import type { Clock } from '@/core/Clock.ts';
import type { Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import type { EventBus } from '@/core/EventBus.ts';
import type { Chat, Counts, Entry, Stream } from '@/model/types.ts';
import type { CafardApi } from '@/net/CafardApi.ts';
import type { FeedEvents } from '@/net/LiveFeed.ts';
import { ApiError } from '@/net/ApiError.ts';
import { chatHref, galleryImagesLabel, imagesLabel, messageTime, previewText } from '@/render/format.ts';
import { ChatRowRenderer } from '@/render/renderers/ChatRowRenderer.ts';
import { Component } from '@/components/Component.ts';
import { popupMenu, type MenuItem } from '@/components/PopupMenu.ts';

/** The sections of the list a chat goes in. */
function sectionOf(chat: Chat): string {
  if (chat.kind === 'direct') return 'direct';
  if (chat.archived) return 'archived';
  return chat.followed ? 'channels' : 'unfollowed';
}

/**
 * The chat list (PLAN 9.6.3), on home and in a chat's list pane: badges
 * from counts, rows from chat events, previews from new messages, status
 * dots from stream_status, a filter field, and a menu on long press or
 * right click (pin, mute, follow, mark read, settings).
 */
export class ChatList extends Component {
  private pressTimer: Disposable | null = null;

  constructor(
    root: HTMLElement,
    private readonly rows: ChatRowRenderer | null,
    private readonly events: EventBus<FeedEvents>,
    private readonly api: CafardApi,
    private readonly clock: Clock,
    private readonly toast: (text: string) => void,
    private readonly search: HTMLInputElement | null,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.add(this.events.on('counts', (c) => this.counts(c)));
    this.bag.add(this.events.on('chat', (c) => this.chat(c)));
    this.bag.add(this.events.on('entry', (e) => this.entry(e)));
    this.bag.add(this.events.on('stream_status', (s) => this.status(s)));
    this.bag.listen(this.root, 'contextmenu', (event) => {
      const row = (event.target as Element | null)?.closest<HTMLElement>('a.chat-row');
      if (row === null || row === undefined) return;
      event.preventDefault();
      this.menu(row);
    });
    this.bag.listen(this.root, 'touchstart', (event) => {
      const row = (event.target as Element | null)?.closest<HTMLElement>('a.chat-row');
      if (row === null || row === undefined) return;
      this.pressTimer = this.clock.setTimeout(() => this.menu(row), 550);
    }, { passive: true });
    const cancelPress = (): void => {
      this.pressTimer?.dispose();
      this.pressTimer = null;
    };
    for (const type of ['touchend', 'touchmove', 'touchcancel']) this.bag.listen(this.root, type, cancelPress, { passive: true });
    this.bag.defer(cancelPress);
    if (this.search !== null) {
      const search = this.search;
      this.bag.listen(search, 'input', () => this.filter(search.value));
    }
  }

  counts(c: Counts): void {
    const byId = new Map(c.chats.map((x) => [x.id, x]));
    for (const row of Dom.findAll<HTMLElement>('a.chat-row', this.root)) {
      const n = byId.get(row.dataset['chat'] ?? '');
      ChatRowRenderer.badges(row, n?.unread ?? 0, n?.open_questions ?? 0);
    }
  }

  /** A chat changed: refresh its row, or add it to its section. */
  chat(chat: Chat): void {
    const rows = Dom.findAll<HTMLElement>('a.chat-row', this.root).filter((r) => r.dataset['chat'] === chat.id);
    if (rows.length > 0) {
      for (const row of rows) this.rows?.fill(row, chat);
      return;
    }
    const name = sectionOf(chat);
    const section = Dom.findAll<HTMLElement>(`[data-section="${name}"]`, this.root)
      .find((el) => name === 'direct' || el.dataset['project'] === chat.project);
    if (section !== undefined && this.rows !== null) section.prepend(this.rows.render(chat));
  }

  /** A new message: its row's preview and time, and the row moves up. */
  entry(e: Entry): void {
    if (e.kind !== 'message' || e.chat === undefined) return;
    const first = e.text.trim().split('\n')[0] ?? '';
    const images = imagesLabel(e.attachments?.filter((a) => a.type === 'image').length ?? 0);
    const text = first !== '' ? first : images !== '' ? images : galleryImagesLabel(e.gallery_refs?.length ?? 0);
    for (const row of Dom.findAll<HTMLElement>('a.chat-row', this.root).filter((r) => r.dataset['chat'] === e.chat)) {
      Dom.setText(Dom.find('[data-slot="preview"]', row), previewText({ ...(e.sender === undefined ? {} : { sender: e.sender.name }), text }));
      Dom.setText(Dom.find('[data-slot="time"]', row), messageTime(e.ts, this.clock.now()));
      const section = row.parentElement;
      if (section === null || row.hasAttribute('data-pinned') || section.dataset['section'] === undefined) continue;
      const firstUnpinned = Array.from(section.children).find((c) => !c.hasAttribute('data-pinned'));
      if (firstUnpinned !== row) section.insertBefore(row, firstUnpinned ?? null);
    }
  }

  status(s: Stream): void {
    for (const dot of Dom.findAll<HTMLElement>('.chat-dots .stream-dot', this.root)) {
      if ((dot.getAttribute('title') ?? '').startsWith(`${s.id}:`)) {
        dot.dataset['status'] = s.status;
        dot.setAttribute('title', `${s.id}: ${s.status}`);
      }
    }
  }

  filter(text: string): void {
    const t = text.trim().toLowerCase();
    for (const row of Dom.findAll<HTMLElement>('a.chat-row', this.root)) {
      const name = Dom.find('[data-slot="name"]', row)?.textContent?.toLowerCase() ?? '';
      row.hidden = t !== '' && !name.includes(t);
    }
  }

  private menu(row: HTMLElement): void {
    const id = row.dataset['chat'] ?? '';
    const channel = id.startsWith('chan:');
    const pinned = row.hasAttribute('data-pinned');
    const muted = row.hasAttribute('data-muted');
    const run = (label: string, action: () => Promise<unknown>): MenuItem => ({
      label,
      run: () => void action().catch((error: unknown) => this.toast(error instanceof ApiError ? error.message : 'That did not work.')),
    });
    popupMenu(this.root, row, 'chat-popup', [
      run(pinned ? 'Unpin' : 'Pin', () => this.api.setPrefs(id, { pinned: !pinned }).then((c) => this.chat(c))),
      run(muted ? 'Unmute' : 'Mute', () => this.api.setPrefs(id, { muted: !muted }).then((c) => this.chat(c))),
      channel ? run(row.closest('[data-section="unfollowed"]') === null ? 'Unfollow' : 'Follow', () => this.api.setPrefs(id, { followed: row.closest('[data-section="unfollowed"]') !== null })) : null,
      run('Mark read', () => this.api.markRead(id, Number.MAX_SAFE_INTEGER)),
      channel ? { label: 'Settings', href: `${chatHref(id)}#settings` } : null,
    ]);
  }
}
