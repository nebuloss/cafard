import { disposable, type Disposable } from '@/core/Disposable.ts';
import type { FrameScheduler } from '@/core/FrameScheduler.ts';

/** A FrameScheduler whose frames run when the test says so. */
export class FakeFrames implements FrameScheduler {
  private queue: (() => void)[] = [];
  requested = 0;

  request(fn: () => void): Disposable {
    this.requested++;
    this.queue.push(fn);
    return disposable(() => {
      this.queue = this.queue.filter((f) => f !== fn);
    });
  }

  get pending(): number {
    return this.queue.length;
  }

  /** Runs the frames requested so far; returns how many ran. */
  run(): number {
    const due = this.queue;
    this.queue = [];
    for (const fn of due) fn();
    return due.length;
  }
}
