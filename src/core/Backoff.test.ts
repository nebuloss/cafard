import { describe, expect, it } from 'vitest';
import { Backoff } from '@/core/Backoff.ts';

describe('Backoff', () => {
  it('doubles up to the cap, with the lowest jitter', () => {
    const b = new Backoff({ baseMs: 1000, maxMs: 8000, random: () => 0 });
    expect([b.next(), b.next(), b.next(), b.next(), b.next()]).toEqual([500, 1000, 2000, 4000, 4000]);
    expect(b.attempts).toBe(5);
  });

  it('stays within the upper half of each step', () => {
    const b = new Backoff({ baseMs: 1000, maxMs: 30000, random: () => 0.999999 });
    expect(b.next()).toBe(1000);
    expect(b.next()).toBe(2000);
  });

  it('starts over after reset', () => {
    const b = new Backoff({ baseMs: 100, maxMs: 1000, random: () => 0 });
    b.next();
    b.next();
    b.reset();
    expect(b.next()).toBe(50);
  });

  it('refuses nonsense bounds', () => {
    expect(() => new Backoff({ baseMs: 0, maxMs: 10 })).toThrow(RangeError);
    expect(() => new Backoff({ baseMs: 10, maxMs: 5 })).toThrow(RangeError);
  });
});
