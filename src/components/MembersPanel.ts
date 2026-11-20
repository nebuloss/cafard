import type { Clock } from '@/core/Clock.ts';
import type { Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import type { EventBus } from '@/core/EventBus.ts';
import { StorageKey, type KeyValueStore } from '@/core/KeyValueStore.ts';
import type { Entry, Member, MemberList } from '@/model/types.ts';
import type { CafardApi } from '@/net/CafardApi.ts';
import type { FeedEvents } from '@/net/LiveFeed.ts';
import { followsText, memberSeen } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';
import { Component } from '@/components/Component.ts';

const isBoolean = (raw: unknown): boolean | null => (typeof raw === 'boolean' ? raw : null);

/** Whether the reader hid the members column (wide screens). */
export const MEMBERS_HIDDEN = new StorageKey<boolean>('cafard.membersHidden', isBoolean);

/** What the panel may set on a tpl-member root. */
export const MEMBER_ATTRIBUTES = ['href', 'data-kind', 'data-presence', 'data-following'] as const;

/** How often a shown panel asks for its members again. */
export const MEMBERS_REFRESH_MS = 60_000;

/** A row of the panel from tpl-member, like pages.Member. */
export function renderMember(template: SlotTemplate, m: Member, nowMs: number): HTMLElement {
  return template
    .clone()
    .attr({
      href: m.can_dm ? `/dm/${encodeURIComponent(m.name)}` : null,
      'data-kind': m.kind,
      'data-presence': m.online ? 'online' : 'offline',
      'data-following': m.following,
    })
    .text('initial', (Array.from(m.name)[0] ?? '?').toUpperCase())
    .text('name', m.name)
    .text('seen', memberSeen(m, nowMs))
    .text('follows', followsText(m.following)).root;
}

/**
 * The members panel of a channel (DECISIONS "a Discord-like left bar"):
 * who reads it, people, agents and devices, each a link to a direct
 * conversation when the reader may open one. From 840 px a column the top
 * bar's toggle shows and hides, remembered; below, a side sheet the same
 * toggle opens (a link to #members without the bundle), closed by its
 * close link, the scrim or Esc. While shown it refreshes every minute,
 * and soon after someone it shows offline writes in the channel.
 */
export class MembersPanel extends Component {
  private readonly page: HTMLElement | null;
  private readonly channel: string;
  private online = new Set<string>();
  private timer: Disposable | null = null;
  private loading = false;

  constructor(
    root: HTMLElement,
    private readonly doc: Document,
    private readonly api: CafardApi,
    private readonly events: EventBus<FeedEvents>,
    private readonly clock: Clock,
    private readonly storage: KeyValueStore,
    private readonly template: SlotTemplate | null,
    /** Whether the panel is a column (840 px and up) rather than a sheet. */
    private readonly wide: () => boolean,
  ) {
    super(root);
    this.page = root.closest<HTMLElement>('.chat-page');
    this.channel = root.dataset['channel'] ?? '';
  }

  protected bind(): void {
    for (const m of Dom.findAll<HTMLElement>('.member[data-presence="online"] [data-slot="name"]', this.root)) this.online.add(m.textContent ?? '');
    for (const toggle of this.toggles()) {
      this.bag.listen(toggle, 'click', (event) => {
        event.preventDefault();
        this.toggle();
      });
    }
    for (const close of Dom.findAll('[data-action="close-members"]', this.doc)) {
      this.bag.listen(close, 'click', (event) => {
        event.preventDefault();
        this.closeSheet();
      });
    }
    this.bag.listen(this.doc, 'keydown', (event) => {
      if ((event as KeyboardEvent).key === 'Escape' && this.sheetOpen()) this.closeSheet();
    });
    this.bag.add(this.events.on('entry', (e) => this.entry(e)));
    this.bag.defer(() => this.timer?.dispose());
    this.setHidden(this.storage.get(MEMBERS_HIDDEN) === true, false);
    const win = this.doc.defaultView;
    if (win?.location.hash === '#members') {
      win.history.replaceState(win.history.state, '', `${win.location.pathname}${win.location.search}`);
      if (!this.wide()) this.openSheet();
    }
    this.schedule();
  }

  private toggles(): HTMLElement[] {
    return Dom.findAll<HTMLElement>('[data-action="members"]', this.doc);
  }

  /** The column is hidden (wide screens). */
  isHidden(): boolean {
    return this.page?.hasAttribute('data-members-hidden') ?? false;
  }

  sheetOpen(): boolean {
    return this.root.hasAttribute('data-open');
  }

  /** Whether the reader sees the panel now. */
  shown(): boolean {
    return this.wide() ? !this.isHidden() : this.sheetOpen();
  }

  toggle(): void {
    if (this.wide()) this.setHidden(!this.isHidden(), true);
    else if (this.sheetOpen()) this.closeSheet();
    else this.openSheet();
  }

  private setHidden(hidden: boolean, save: boolean): void {
    if (hidden) this.page?.setAttribute('data-members-hidden', '');
    else this.page?.removeAttribute('data-members-hidden');
    for (const t of this.toggles()) t.setAttribute('aria-pressed', String(!hidden));
    if (!save) return;
    this.storage.set(MEMBERS_HIDDEN, hidden);
    if (!hidden) void this.refresh();
  }

  openSheet(): void {
    this.root.setAttribute('data-open', '');
    for (const t of this.toggles()) t.setAttribute('aria-expanded', 'true');
    Dom.find<HTMLElement>('a.member[href], [data-action="close-members"]', this.root)?.focus({ preventScroll: true });
    void this.refresh();
  }

  closeSheet(): void {
    if (!this.sheetOpen()) return;
    this.root.removeAttribute('data-open');
    const toggles = this.toggles();
    for (const t of toggles) t.setAttribute('aria-expanded', 'false');
    toggles[0]?.focus({ preventScroll: true });
  }

  /** Asks for the members again, while the panel shows. */
  private schedule(): void {
    this.timer?.dispose();
    this.timer = this.clock.setTimeout(() => {
      if (this.shown()) void this.refresh();
      this.schedule();
    }, MEMBERS_REFRESH_MS);
  }

  /** A message in the channel from someone shown offline: they are online now. */
  private entry(e: Entry): void {
    if (e.chat !== `chan:${this.channel}` || e.sender === undefined || this.online.has(e.sender.name) || !this.shown()) return;
    void this.refresh();
  }

  async refresh(): Promise<void> {
    if (this.loading || this.channel === '') return;
    this.loading = true;
    try {
      this.render(await this.api.channelMembers(this.channel));
    } catch {
      // The rows the server rendered, or the last ones, stay.
    } finally {
      this.loading = false;
    }
  }

  render(list: MemberList): void {
    const template = this.template;
    if (template === null) return;
    const now = this.clock.now();
    this.online = new Set(list.members.filter((m) => m.online).map((m) => m.name));
    for (const group of Dom.findAll<HTMLElement>('[data-group]', this.root)) {
      const members = list.members.filter((m) => m.kind === group.dataset['group']);
      Dom.find('[data-slot="rows"]', group)?.replaceChildren(...members.map((m) => renderMember(template, m, now)));
      Dom.setText(Dom.find('[data-slot="count"]', group), String(members.length));
      group.hidden = members.length === 0;
    }
  }
}
