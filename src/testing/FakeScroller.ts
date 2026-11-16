import { disposable, type Disposable } from '@/core/Disposable.ts';
import type { Scroller } from '@/components/LogView.ts';

/** A Scroller the test moves by hand; toEnd fires a scroll like a browser does. */
export class FakeScroller implements Scroller {
  end = true;
  toEndCalls = 0;
  private listener: (() => void) | null = null;

  atEnd(): boolean {
    return this.end;
  }

  toEnd(): void {
    this.toEndCalls++;
    this.end = true;
    this.listener?.();
  }

  onScroll(listener: () => void): Disposable {
    this.listener = listener;
    return disposable(() => (this.listener = null));
  }

  userScrolls(end: boolean): void {
    this.end = end;
    this.listener?.();
  }
}
