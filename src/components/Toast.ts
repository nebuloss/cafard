import type { Clock } from '@/core/Clock.ts';
import type { Disposable } from '@/core/Disposable.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { Component } from '@/components/Component.ts';

export type ToastTone = 'info' | 'error';

/** Snackbars in the toast region, from tpl-toast. At most three at once. */
export class Toast extends Component {
  static readonly showFor = 5000;
  private static readonly max = 3;
  private readonly template: SlotTemplate | null;
  private readonly timers = new Map<HTMLElement, Disposable>();

  constructor(
    root: HTMLElement,
    private readonly clock: Clock,
    doc: Document,
  ) {
    super(root);
    this.template = SlotTemplate.find(doc, 'tpl-toast', ['data-tone']);
  }

  protected bind(): void {
    // Server-rendered notices go away like the others.
    for (const el of Array.from(this.root.children)) {
      if (el instanceof HTMLElement) this.expire(el);
    }
    this.bag.defer(() => {
      for (const t of this.timers.values()) t.dispose();
      this.timers.clear();
    });
  }

  /** Shows text as given: the server owns the wording of errors. */
  show(text: string, tone: ToastTone = 'info'): HTMLElement | null {
    if (this.template === null || this.bag.isDisposed) return null;
    const t = this.template.clone().attr({ 'data-tone': tone }).text('text', text);
    this.root.append(t.root);
    while (this.root.children.length > Toast.max) {
      const first = this.root.firstElementChild;
      if (first instanceof HTMLElement) this.remove(first);
      else break;
    }
    this.expire(t.root);
    return t.root;
  }

  private expire(el: HTMLElement): void {
    this.timers.set(el, this.clock.setTimeout(() => this.remove(el), Toast.showFor));
  }

  private remove(el: HTMLElement): void {
    this.timers.get(el)?.dispose();
    this.timers.delete(el);
    el.remove();
  }
}
