import { disposable, type Disposable } from '@/core/Disposable.ts';
import type { Scroller } from '@/components/LogView.ts';

/** A Scroller over a pane that scrolls on its own (the chat's panes). */
export class ElementScroller implements Scroller {
  constructor(
    private readonly el: HTMLElement,
    private readonly slack = 48,
  ) {}

  atEnd(): boolean {
    return this.el.scrollTop + this.el.clientHeight >= this.el.scrollHeight - this.slack;
  }

  atStart(): boolean {
    return this.el.scrollTop <= this.slack * 4;
  }

  toEnd(): void {
    this.el.scrollTop = this.el.scrollHeight;
  }

  /** Keeps what the reader sees in place while rows are added above it. */
  keepPosition(change: () => void): void {
    const before = this.el.scrollHeight - this.el.scrollTop;
    change();
    this.el.scrollTop = this.el.scrollHeight - before;
  }

  onScroll(listener: () => void): Disposable {
    this.el.addEventListener('scroll', listener, { passive: true });
    return disposable(() => this.el.removeEventListener('scroll', listener));
  }
}
