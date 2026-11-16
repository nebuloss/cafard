import { describe, expect, it, vi } from 'vitest';
import { EventBus } from '@/core/EventBus.ts';

interface Events {
  message: { seq: number };
  closed: undefined;
}

describe('EventBus', () => {
  it('delivers payloads to handlers of that type only', () => {
    const bus = new EventBus<Events>();
    const onMessage = vi.fn();
    const onClosed = vi.fn();
    bus.on('message', onMessage);
    bus.on('closed', onClosed);
    bus.emit('message', { seq: 7 });
    expect(onMessage).toHaveBeenCalledWith({ seq: 7 });
    expect(onClosed).not.toHaveBeenCalled();
  });

  it('stops delivering once the subscription is disposed', () => {
    const bus = new EventBus<Events>();
    const handler = vi.fn();
    const sub = bus.on('message', handler);
    sub.dispose();
    bus.emit('message', { seq: 1 });
    expect(handler).not.toHaveBeenCalled();
    expect(bus.listenerCount('message')).toBe(0);
  });

  it('isolates a throwing handler', () => {
    const errors: unknown[] = [];
    const bus = new EventBus<Events>((e) => errors.push(e));
    const second = vi.fn();
    bus.on('message', () => {
      throw new Error('bad');
    });
    bus.on('message', second);
    bus.emit('message', { seq: 1 });
    expect(second).toHaveBeenCalled();
    expect(errors).toHaveLength(1);
  });

  it('lets a handler unsubscribe itself while emitting', () => {
    const bus = new EventBus<Events>();
    const later = vi.fn();
    const sub = bus.on('message', () => sub.dispose());
    bus.on('message', later);
    bus.emit('message', { seq: 1 });
    bus.emit('message', { seq: 2 });
    expect(later).toHaveBeenCalledTimes(2);
    expect(bus.listenerCount('message')).toBe(1);
  });
});
