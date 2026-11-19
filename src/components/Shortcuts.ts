import { Dom } from '@/core/Dom.ts';
import { Component } from '@/components/Component.ts';
import { isTyping } from '@/components/FilterBar.ts';
import { closeDialog, openDialog } from '@/components/ImageViewer.ts';

/** Where each "g" chord goes (PLAN 9.6.9): projects, direct messages, Live, Sources. */
const GO: Readonly<Record<string, string>> = { p: '/', d: '/dm', l: '/live', s: '/streams' };

/**
 * The desktop keys (PLAN 9.6.9): "/" focuses the filter or the composer,
 * "g p", "g d", "g l", "g s" go to the projects, the direct
 * messages, Live and Sources, "?" lists the keys,
 * Esc closes what is open (dialogs close on Esc by themselves). They wait
 * while a field has focus, except Esc and Ctrl+Enter, which their fields
 * handle.
 */
export class Shortcuts extends Component<HTMLElement> {
  private chord = false;

  constructor(
    root: HTMLElement,
    private readonly doc: Document,
    private readonly go: (path: string) => void,
    private readonly focusTarget: () => HTMLElement | null,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.listen(this.doc, 'keydown', (event) => this.key(event as KeyboardEvent));
    this.bag.listen(this.root, 'click', (event) => {
      if ((event.target as Element | null)?.closest('[data-action="close"]') !== null && this.root instanceof HTMLDialogElement) closeDialog(this.root);
    });
  }

  key(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      for (const popup of Dom.findAll('.msg-popup, .chat-popup', this.doc)) popup.remove();
      for (const menu of Dom.findAll('[data-slot="line-menu"]', this.doc)) menu.hidden = true;
      return;
    }
    if (e.ctrlKey || e.metaKey || e.altKey || isTyping(e.target)) return;
    if (this.chord) {
      this.chord = false;
      const path = GO[e.key];
      if (path !== undefined) {
        e.preventDefault();
        this.go(path);
      }
      return;
    }
    if (e.key === 'g') {
      this.chord = true;
      return;
    }
    if (e.key === '?' && this.root instanceof HTMLDialogElement) {
      e.preventDefault();
      openDialog(this.root);
      return;
    }
    if (e.key === '/') {
      const target = this.focusTarget();
      if (target !== null) {
        e.preventDefault();
        target.focus();
      }
    }
  }
}
