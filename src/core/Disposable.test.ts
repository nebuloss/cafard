import { describe, expect, it, vi } from 'vitest';
import { disposable, DisposableBag } from '@/core/Disposable.ts';

describe('disposable', () => {
  it('runs its cleanup once', () => {
    const cleanup = vi.fn();
    const d = disposable(cleanup);
    d.dispose();
    d.dispose();
    expect(cleanup).toHaveBeenCalledTimes(1);
  });
});

describe('DisposableBag', () => {
  it('disposes newest first', () => {
    const order: number[] = [];
    const bag = new DisposableBag();
    bag.defer(() => order.push(1));
    bag.defer(() => order.push(2));
    bag.dispose();
    expect(order).toEqual([2, 1]);
    expect(bag.isDisposed).toBe(true);
  });

  it('disposes at once what is added after disposal', () => {
    const bag = new DisposableBag();
    bag.dispose();
    const cleanup = vi.fn();
    bag.defer(cleanup);
    expect(cleanup).toHaveBeenCalledTimes(1);
  });

  it('keeps disposing after a failure and rethrows it', () => {
    const bag = new DisposableBag();
    const after = vi.fn();
    bag.defer(after);
    bag.defer(() => {
      throw new Error('boom');
    });
    expect(() => bag.dispose()).toThrow('boom');
    expect(after).toHaveBeenCalled();
  });

  it('removes listeners it added', () => {
    const bag = new DisposableBag();
    const target = new EventTarget();
    const handler = vi.fn();
    bag.listen(target, 'ping', handler);
    target.dispatchEvent(new Event('ping'));
    bag.dispose();
    target.dispatchEvent(new Event('ping'));
    expect(handler).toHaveBeenCalledTimes(1);
  });
});
