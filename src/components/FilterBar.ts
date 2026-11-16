import { Dom } from '@/core/Dom.ts';
import { Component } from '@/components/Component.ts';

/**
 * The logs filters. The form is a plain GET, so filters live in the URL and
 * work without the bundle; this only opens it inline from medium screens up
 * (a sheet behind one button on a phone) and gives "/" focus to it.
 */
export class FilterBar extends Component<HTMLDetailsElement> {
  constructor(
    root: HTMLDetailsElement,
    private readonly wide: MediaQueryList | null,
    private readonly doc: Document,
  ) {
    super(root);
  }

  protected bind(): void {
    if (this.wide !== null) {
      const apply = (): void => {
        if (this.wide?.matches === true) this.root.open = true;
      };
      apply();
      this.bag.listen(this.wide as unknown as EventTarget, 'change', apply);
    }
    this.bag.listen(this.doc, 'keydown', (event) => {
      const e = event as KeyboardEvent;
      if (e.key !== '/' || isTyping(e.target)) return;
      const input = Dom.find<HTMLInputElement>('input[name="streams"]', this.root);
      if (input === null) return;
      e.preventDefault();
      this.root.open = true;
      input.focus();
    });
  }
}

export function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName);
}
