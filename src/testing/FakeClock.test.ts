import { describe, expect, it } from 'vitest';
import { FakeClock } from '@/testing/FakeClock.ts';

describe('FakeClock', () => {
  it('runs timers in time order, including ones scheduled while advancing', () => {
    const clock = new FakeClock(1000);
    const log: string[] = [];
    clock.setTimeout(() => log.push(`b@${clock.now()}`), 20);
    clock.setTimeout(() => {
      log.push(`a@${clock.now()}`);
      clock.setTimeout(() => log.push(`c@${clock.now()}`), 5);
    }, 10);
    clock.advance(30);
    expect(log).toEqual(['a@1010', 'c@1015', 'b@1020']);
    expect(clock.now()).toBe(1030);
  });

  it('cancels a disposed timer', () => {
    const clock = new FakeClock();
    let ran = false;
    clock.setTimeout(() => (ran = true), 5).dispose();
    clock.advance(10);
    expect(ran).toBe(false);
    expect(clock.pending).toBe(0);
  });
});
