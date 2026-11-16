import { describe, expect, it, vi } from 'vitest';
import { Observable } from '@/core/Observable.ts';

describe('Observable', () => {
  it('notifies on change with the previous value', () => {
    const state = new Observable('connecting');
    const listener = vi.fn();
    state.subscribe(listener);
    state.set('live');
    expect(listener).toHaveBeenCalledWith('live', 'connecting');
    expect(state.value).toBe('live');
  });

  it('does not notify when the value is equal', () => {
    const state = new Observable(1);
    const listener = vi.fn();
    state.subscribe(listener);
    state.set(1);
    expect(listener).not.toHaveBeenCalled();
  });

  it('uses the given equality', () => {
    const state = new Observable({ n: 1 }, (a, b) => a.n === b.n);
    const listener = vi.fn();
    state.subscribe(listener);
    state.set({ n: 1 });
    state.update((v) => ({ n: v.n + 1 }));
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it('runs immediately when asked, and stops after dispose', () => {
    const state = new Observable(0);
    const listener = vi.fn();
    const sub = state.subscribe(listener, { immediate: true });
    expect(listener).toHaveBeenCalledWith(0, 0);
    sub.dispose();
    state.set(5);
    expect(listener).toHaveBeenCalledTimes(1);
  });
});
