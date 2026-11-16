import { disposable, type Disposable } from '@/core/Disposable.ts';

/** Time, injected so tests can move it by hand. */
export interface Clock {
  /** Milliseconds since the epoch. */
  now(): number;
  /** Runs fn once after ms. Disposing cancels it. */
  setTimeout(fn: () => void, ms: number): Disposable;
}

export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }

  setTimeout(fn: () => void, ms: number): Disposable {
    const id = globalThis.setTimeout(fn, ms);
    return disposable(() => globalThis.clearTimeout(id));
  }
}
