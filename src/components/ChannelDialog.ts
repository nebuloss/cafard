import type { Clock } from '@/core/Clock.ts';
import type { Disposable } from '@/core/Disposable.ts';
import { Dom, h } from '@/core/Dom.ts';
import type { Chat, ChannelUpdate, Surface } from '@/model/types.ts';
import type { CafardApi } from '@/net/CafardApi.ts';
import { ApiError } from '@/net/ApiError.ts';
import { Component } from '@/components/Component.ts';
import { closeDialog, openDialog } from '@/components/ImageViewer.ts';

/** The update a settings form asks for, against what the chat has. */
export function channelUpdate(
  chat: Pick<Chat, 'title' | 'topic' | 'surface' | 'archived'>,
  form: { title: string; topic: string; surface: string; rename: string; archived: boolean | null },
  added: readonly string[],
  removed: readonly string[],
): ChannelUpdate {
  const u: { -readonly [K in keyof ChannelUpdate]: ChannelUpdate[K] } = {};
  if (form.title !== (chat.title ?? '')) u.title = form.title;
  if (form.topic !== (chat.topic ?? '')) u.topic = form.topic;
  if (form.surface !== chat.surface && ['none', 'markers', 'errors'].includes(form.surface)) u.surface = form.surface as Surface;
  if (form.rename.trim() !== '') u.rename = form.rename.trim();
  if (form.archived !== null && form.archived !== chat.archived) u.archived = form.archived;
  if (added.length > 0) u.add_patterns = added;
  if (removed.length > 0) u.remove_patterns = removed;
  return u;
}

/**
 * Channel settings (PLAN 9.6.3): title, topic, bindings with a preview of
 * what a glob matches now, surface, follow, pin, mute, and for its creator
 * or an admin rename, archive and delete. Errors show the server's words.
 */
export class ChannelDialog extends Component<HTMLDialogElement> {
  private added: string[] = [];
  private removed: string[] = [];
  private chat: Chat | null = null;
  private previewTimer: Disposable | null = null;

  constructor(
    root: HTMLDialogElement,
    private readonly name: string,
    private readonly api: CafardApi,
    private readonly clock: Clock,
    private readonly go: (path: string) => void,
    private readonly confirm: (question: string) => boolean,
    private readonly changed: (chat: Chat) => void,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.listen(this.root, 'click', (event) => {
      const el = (event.target as Element | null)?.closest<HTMLElement>('[data-action]');
      switch (el?.dataset['action']) {
        case 'close':
          closeDialog(this.root);
          break;
        case 'bind':
          this.bindPattern();
          break;
        case 'unbind':
          this.unbind(el.dataset['pattern'] ?? '');
          break;
        case 'save':
          void this.save();
          break;
        case 'delete':
          void this.remove();
          break;
      }
    });
    const pattern = this.input('pattern');
    if (pattern !== null) {
      this.bag.listen(pattern, 'input', () => {
        this.previewTimer?.dispose();
        this.previewTimer = this.clock.setTimeout(() => void this.preview(pattern.value), 250);
      });
    }
    this.bag.defer(() => this.previewTimer?.dispose());
  }

  open(chat?: Chat): void {
    if (chat !== undefined) this.chat = chat;
    this.added = [];
    this.removed = [];
    this.error('');
    openDialog(this.root);
  }

  private input<E extends HTMLElement = HTMLInputElement>(name: string): E | null {
    return Dom.find<E>(`[name="${name}"]`, this.root);
  }

  private error(text: string): void {
    const el = Dom.find('[data-slot="error"]', this.root);
    if (el === null) return;
    el.hidden = text === '';
    Dom.setText(el, text);
  }

  private async preview(pattern: string): Promise<void> {
    const list = Dom.find('[data-slot="preview"]', this.root);
    if (list === null) return;
    if (pattern.trim() === '') {
      list.replaceChildren();
      return;
    }
    try {
      const streams = await this.api.matchStreams(pattern.trim());
      list.replaceChildren(...(streams.length === 0 ? [h('li', { class: 'muted' }, 'matches no stream yet')] : streams.map((s) => h('li', {}, `${s.id} (${s.status})`))));
    } catch (error) {
      list.replaceChildren(h('li', { class: 'error-text' }, error instanceof ApiError ? error.message : 'The pattern could not be checked.'));
    }
  }

  private bindPattern(): void {
    const input = this.input('pattern');
    const p = input?.value.trim() ?? '';
    if (p === '' || input === null) return;
    this.removed = this.removed.filter((x) => x !== p);
    if (!this.added.includes(p)) this.added.push(p);
    Dom.find('[data-slot="patterns"]', this.root)?.append(
      h('li', { 'data-pattern': p }, h('span', { class: 'mono' }, p), h('button', { class: 'btn text danger-text', type: 'button', 'data-action': 'unbind', 'data-pattern': p }, 'Remove')),
    );
    input.value = '';
    Dom.find('[data-slot="preview"]', this.root)?.replaceChildren();
  }

  private unbind(p: string): void {
    if (this.added.includes(p)) this.added = this.added.filter((x) => x !== p);
    else if (!this.removed.includes(p)) this.removed.push(p);
    for (const li of Dom.findAll('[data-slot="patterns"] li', this.root)) if (li.dataset['pattern'] === p) li.remove();
  }

  private async save(): Promise<void> {
    const chat = this.chat;
    if (chat === null) return;
    const value = (name: string): string => this.input<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(name)?.value ?? '';
    const checked = (name: string): boolean | null => {
      const el = this.input(name);
      return el === null ? null : el.checked;
    };
    try {
      const update = channelUpdate(chat, { title: value('title'), topic: value('topic'), surface: value('surface'), rename: value('rename'), archived: checked('archived') }, this.added, this.removed);
      let next = chat;
      if (Object.keys(update).length > 0) next = await this.api.updateChannel(this.name, update);
      const prefs: { pinned?: boolean; muted?: boolean; followed?: boolean } = {};
      for (const key of ['pinned', 'muted', 'followed'] as const) {
        const v = checked(key);
        if (v !== null && v !== next[key]) prefs[key] = v;
      }
      if (Object.keys(prefs).length > 0) next = await this.api.setPrefs(next.id, prefs);
      this.chat = next;
      closeDialog(this.root);
      if (next.name !== this.name) this.go(`/c/${encodeURIComponent(next.name)}`);
      else this.changed(next);
    } catch (error) {
      this.error(error instanceof ApiError ? error.message : 'The settings were not saved.');
    }
  }

  private async remove(): Promise<void> {
    if (!this.confirm(`Delete #${this.name} and all its messages? This cannot be undone.`)) return;
    try {
      await this.api.deleteChannel(this.name);
      this.go('/');
    } catch (error) {
      this.error(error instanceof ApiError ? error.message : 'The channel was not deleted.');
    }
  }
}
