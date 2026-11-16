import type { Clock } from '@/core/Clock.ts';
import { disposable, type Disposable } from '@/core/Disposable.ts';

interface Timer {
  readonly id: number;
  readonly at: number;
  readonly fn: () => void;
}

/** A Clock that moves only when the test calls advance(). */
export class FakeClock implements Clock {
  private time: number;
  private nextId = 0;
  private timers: Timer[] = [];

  constructor(start = 0) {
    this.time = start;
  }

  now(): number {
    return this.time;
  }

  setTimeout(fn: () => void, ms: number): Disposable {
    const timer = { id: this.nextId++, at: this.time + Math.max(0, ms), fn };
    this.timers.push(timer);
    return disposable(() => {
      this.timers = this.timers.filter((t) => t !== timer);
    });
  }

  /** Moves time forward, running due timers in order, including ones they schedule. */
  advance(ms: number): void {
    const end = this.time + ms;
    for (;;) {
      const due = this.timers.filter((t) => t.at <= end).sort((a, b) => a.at - b.at || a.id - b.id)[0];
      if (due === undefined) break;
      this.timers = this.timers.filter((t) => t !== due);
      this.time = due.at;
      due.fn();
    }
    this.time = end;
  }

  get pending(): number {
    return this.timers.length;
  }
}
