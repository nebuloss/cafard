import { disposable, type Disposable } from '@/core/Disposable.ts';
import type { Scroller } from '@/components/LogView.ts';

/** How near the end, in pixels, still counts as at the end. */
const SLACK = 48;

/** A Scroller over a pane that scrolls on its own (the chat's panes). */
export class ElementScroller implements Scroller {
  constructor(private readonly el: HTMLElement) {}

  atEnd(): boolean {
    return this.el.scrollTop + this.el.clientHeight >= this.el.scrollHeight - SLACK;
  }

  atStart(): boolean {
    return this.el.scrollTop <= SLACK * 4;
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
