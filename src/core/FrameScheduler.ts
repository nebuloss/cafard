import { disposable, type Disposable } from '@/core/Disposable.ts';

/**
 * When DOM work runs: once per animation frame in a browser, so a fast
 * feed costs one layout per frame however many entries arrive. Tests
 * pass a fake and run the frames by hand.
 */
export interface FrameScheduler {
  /** Runs fn before the next frame. Disposing cancels it. */
  request(fn: () => void): Disposable;
}

/** requestAnimationFrame, or a 16 ms timer where there is none. */
export class AnimationFrames implements FrameScheduler {
  constructor(private readonly win: Window | null) {}

  request(fn: () => void): Disposable {
    const win = this.win;
    if (win !== null && typeof win.requestAnimationFrame === 'function') {
      const id = win.requestAnimationFrame(() => fn());
      return disposable(() => win.cancelAnimationFrame(id));
    }
    const id = globalThis.setTimeout(fn, 16);
    return disposable(() => globalThis.clearTimeout(id));
  }
}
