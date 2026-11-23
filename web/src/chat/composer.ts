import { Component, mayAutofocus } from '@shared/component';
import { Bytes } from '@shared/format';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { ui } from '@shared/ui';
import { Avatar } from '@shared/widgets';
import { MessageText } from './text';
import type { Member } from './types';

/** A file chosen to send, with its preview while it waits. */
class Staged {
  readonly preview: string | null;
  constructor(readonly file: File) {
    this.preview = file.type.startsWith('image/') ? URL.createObjectURL(file) : null;
  }
  drop(): void {
    if (this.preview) URL.revokeObjectURL(this.preview);
  }
}

/**
 * What "File or log" offers: text and documents and archives, by type and by
 * the extensions logs and configurations carry. No image, video or audio
 * type, so a phone opens its files rather than offering a camera or a
 * recorder.
 */
const FILE_TYPES = 'text/*,application/*,.log,.txt,.csv,.json,.yaml,.yml,.toml,.ini,.conf,.cfg,.md,.xml,.dts,.dtb,.hex,.bin,.pdf,.zip,.gz,.tgz,.tar,.xz,.bz2,.zst,.7z,.pcap,.pcapng';

export interface ComposerOptions {
  /** Sends; the composer clears when it resolves, and keeps its text when it throws. */
  send: (text: string, files: File[]) => Promise<void>;
  /** The members @ can name. */
  members: () => Member[];
  placeholder: string;
}

/**
 * Composer is where a message is written: text, files chosen, pasted or
 * dropped, and @ to name a member. "Paste, drop or share, and it is sent"
 * (docs/design.md, "The chat app"): files wait in a strip above the text
 * until Send, so a caption and its photos go as one message.
 */
export class Composer extends Component {
  private staged: Staged[] = [];
  private sending = false;
  private mentions: Member[] = [];
  private selected = 0;

  constructor(private readonly opts: ComposerOptions) {
    // busy, while a message is sent, dims the text.
    super('div', 'relative shrink-0 border-t border-outline-variant bg-surface px-3 pt-2 pb-[calc(8px+env(safe-area-inset-bottom,0px))] min-[840px]:px-5 [&.busy_textarea]:opacity-60');
    this.actions = {
      unstage: (t) => {
        const i = Number(t.dataset['i']);
        this.staged[i]?.drop();
        this.staged.splice(i, 1);
        this.drawStaged();
      },
      mention: (t) => this.insertMention(t.dataset['v'] ?? ''),
      send: () => this.submit(),
      // The attach button asks what is attached, so the browser is told: a
      // phone given a file input that takes anything offers the camera and
      // the microphone too, and asks for their permissions.
      'attach-menu': () => this.toggleAttach(),
      'attach-media': () => this.pick('media'),
      'attach-file': () => this.pick('file'),
    };
    // pointerdown rather than click: a tap elsewhere is a touch that a
    // browser does not always turn into a click.
    document.addEventListener('pointerdown', (e) => {
      if (!this.root.isConnected) return;
      const t = e.target as Element | null;
      if (!t?.closest('.attach-menu, [data-act="attach-menu"]')) this.toggleAttach(false);
    });
    this.root.addEventListener('change', (e) => {
      const t = e.target as HTMLInputElement;
      if (t.type === 'file' && t.files) {
        this.stage([...t.files]);
        t.value = '';
      }
    });
    this.root.addEventListener('input', (e) => {
      if ((e.target as HTMLElement).tagName === 'TEXTAREA') {
        this.autosize();
        this.updateSend();
        this.checkMention();
      }
    });
    this.root.addEventListener('keydown', (e) => this.onKey(e));
    this.root.addEventListener('paste', (e) => {
      const files = [...(e.clipboardData?.files ?? [])];
      if (files.length) {
        e.preventDefault();
        this.stage(files);
      }
    });
  }

  protected view(): Html {
    return html`<div class="attach-menu absolute bottom-[calc(100%-4px)] left-2 z-[5] min-w-52 rounded-xl border border-outline-variant bg-surface-lowest p-1 shadow-[0_8px_24px_rgba(0,0,0,.18)] min-[840px]:left-4" role="menu" hidden>
        <button type="button" class="state-layer flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm" role="menuitem" data-act="attach-media"><svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2"/><circle cx="9" cy="10" r="1.6"/><path d="M21 16l-5-5-8 8"/></svg>Photo or video</button>
        <button type="button" class="state-layer flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm" role="menuitem" data-act="attach-file"><svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4M9 12h6M9 16h6"/></svg>File or log</button></div>
      <div class="mentions absolute bottom-[calc(100%-4px)] left-[60px] z-[5] max-w-[calc(100%-80px)] min-w-60 rounded-xl border border-outline-variant bg-surface-lowest p-1 shadow-[0_8px_24px_rgba(0,0,0,.18)]" hidden></div><div class="staged flex gap-2 overflow-x-auto pb-2 empty:hidden"></div>
      <form class="flex items-end gap-1.5" novalidate>
        <button type="button" class="${ui.iconBtn}" data-act="attach-menu" title="Attach" aria-label="Attach" aria-haspopup="menu" aria-expanded="false"><svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M21 11.5l-8.6 8.6a5 5 0 01-7.1-7.1l8.6-8.6a3.3 3.3 0 014.7 4.7l-8.6 8.6a1.7 1.7 0 01-2.4-2.4l7.9-7.9"/></svg></button>
        <input class="pick-media" type="file" multiple accept="image/*,video/*" hidden>
        <input class="pick-file" type="file" multiple accept="${FILE_TYPES}" hidden>
        <textarea class="max-h-[140px] min-h-[42px] min-w-0 flex-1 resize-none rounded-[21px] border border-outline-variant bg-surface-lowest px-3.5 py-2.5 leading-[1.4] outline-none focus:border-primary" rows="1" placeholder="${this.opts.placeholder}" aria-label="Message"></textarea>
        <button class="send grid size-[42px] shrink-0 place-items-center rounded-full bg-primary text-on-primary disabled:cursor-default disabled:opacity-40" type="button" data-act="send" aria-label="Send" disabled><svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 12l16-8-6 16-2.5-6.5z"/><path d="M11.5 13.5L20 4"/></svg></button>
      </form>`;
  }

  protected override afterRender(): void {
    this.drawStaged();
  }

  private get textarea(): HTMLTextAreaElement {
    return this.$<HTMLTextAreaElement>('textarea');
  }

  /** Puts focus in the message, unless on a touch screen (mayAutofocus). */
  focus(): void {
    if (mayAutofocus()) this.find<HTMLTextAreaElement>('textarea')?.focus();
  }

  private toggleAttach(open?: boolean): void {
    const menu = this.find('.attach-menu');
    const btn = this.find('[data-act="attach-menu"]');
    if (!menu || !btn) return;
    const show = open ?? menu.hidden;
    menu.hidden = !show;
    btn.setAttribute('aria-expanded', String(show));
  }

  /** Opens the browser's chooser for one kind of file: in the click, as browsers require. */
  private pick(kind: 'media' | 'file'): void {
    this.toggleAttach(false);
    this.find<HTMLInputElement>(kind === 'media' ? '.pick-media' : '.pick-file')?.click();
  }

  /** Adds files to send with the next message. */
  stage(files: File[]): void {
    this.staged.push(...files.map((f) => new Staged(f)));
    this.drawStaged();
    this.focus();
  }

  private drawStaged(): void {
    const box = this.find('.staged');
    if (!box) return;
    box.innerHTML = Html.join(this.staged.map((s, i) => {
      const x = html`<button type="button" class="absolute top-[3px] right-[3px] grid size-[22px] place-items-center rounded-full bg-black/45 text-white [&_.ic]:size-3.5" data-act="unstage" data-i="${String(i)}" aria-label="Remove ${s.file.name}">${Icon.of('close')}</button>`;
      const st = 'relative flex h-16 shrink-0 items-center overflow-hidden rounded-[10px] bg-surface-high';
      return s.preview
        ? html`<div class="${st}"><img class="block size-16 object-cover" src="${s.preview}" alt="">${x}</div>`
        : html`<div class="${st}"><div class="flex max-w-[220px] items-center gap-1.5 pr-[30px] pl-2.5 text-[12.5px] font-semibold"><span class="truncate">${s.file.name} · ${Bytes.format(s.file.size)}</span></div>${x}</div>`;
    })).value;
    this.updateSend();
  }

  private updateSend(): void {
    const btn = this.find<HTMLButtonElement>('.send');
    if (btn) btn.disabled = this.sending || (!this.textarea.value.trim() && !this.staged.length);
  }

  private autosize(): void {
    const t = this.textarea;
    t.style.height = 'auto';
    t.style.height = `${Math.min(t.scrollHeight + 2, 160)}px`;
  }

  private onKey(e: KeyboardEvent): void {
    if ((e.target as HTMLElement).tagName !== 'TEXTAREA') return;
    if (this.mentions.length) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        this.selected = (this.selected + (e.key === 'ArrowDown' ? 1 : -1) + this.mentions.length) % this.mentions.length;
        this.drawMentions();
        return;
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        const m = this.mentions[this.selected];
        if (m) this.insertMention(m.name);
        return;
      }
      if (e.key === 'Escape') {
        e.stopPropagation();
        this.hideMentions();
        return;
      }
    }
    // On a phone, Enter makes a new line and the button sends: a keyboard
    // without Shift would otherwise have no way to write two lines.
    const touch = window.matchMedia('(pointer: coarse)').matches;
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && !touch) {
      e.preventDefault();
      void this.submit();
    }
  }

  private checkMention(): void {
    const t = this.textarea;
    const typed = MessageText.mentionAt(t.value.slice(0, t.selectionStart));
    if (typed === null) return this.hideMentions();
    this.mentions = this.opts.members().filter((m) => m.name.startsWith(typed) || (m.display_name ?? '').toLowerCase().startsWith(typed)).slice(0, 8);
    if (!this.mentions.length) return this.hideMentions();
    this.selected = Math.min(this.selected, this.mentions.length - 1);
    this.drawMentions();
  }

  private drawMentions(): void {
    const box = this.$('.mentions');
    box.hidden = false;
    box.innerHTML = Html.join(this.mentions.map((m, i) =>
      html`<button type="button" class="flex w-full items-center gap-2.5 rounded-md3-s px-2 py-1.5 text-left hover:bg-surface-container ${i === this.selected ? 'bg-surface-container' : ''}" data-act="mention" data-v="${m.name}">${Avatar.of(m, 'sm')}<span><b>${m.display_name || m.name}</b><small class="block text-xs text-on-surface-variant">@${m.name}${m.kind !== 'person' ? ` · ${m.kind}` : ''}</small></span></button>`)).value;
  }

  private hideMentions(): void {
    this.mentions = [];
    this.selected = 0;
    const box = this.find('.mentions');
    if (box) box.hidden = true;
  }

  private insertMention(name: string): void {
    const t = this.textarea;
    const pos = t.selectionStart;
    const before = t.value.slice(0, pos).replace(/@([a-z0-9._-]*)$/i, `@${name} `);
    t.value = before + t.value.slice(pos);
    t.selectionStart = t.selectionEnd = before.length;
    this.hideMentions();
    this.updateSend();
    t.focus();
  }

  private async submit(): Promise<void> {
    const text = this.textarea.value;
    if (this.sending || (!text.trim() && !this.staged.length)) return;
    this.sending = true;
    this.updateSend();
    this.root.classList.add('busy');
    try {
      await this.opts.send(text, this.staged.map((s) => s.file));
      this.staged.forEach((s) => s.drop());
      this.staged = [];
      this.textarea.value = '';
      this.autosize();
      this.drawStaged();
    } finally {
      this.sending = false;
      this.root.classList.remove('busy');
      this.updateSend();
    }
  }
}
