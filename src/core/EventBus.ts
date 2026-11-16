import { disposable, type Disposable } from '@/core/Disposable.ts';

type Handler<P> = (payload: P) => void;

/**
 * Typed publish/subscribe. EventMap maps an event name to its payload type,
 * so emitting the wrong payload is a compile error.
 */
export class EventBus<EventMap extends object> {
  private readonly handlers = new Map<keyof EventMap, Set<Handler<never>>>();

  constructor(private readonly onError: (error: unknown) => void = (error) => console.error('cafard:', error)) {}

  on<K extends keyof EventMap>(type: K, handler: Handler<EventMap[K]>): Disposable {
    let set = this.handlers.get(type);
    if (set === undefined) {
      set = new Set();
      this.handlers.set(type, set);
    }
    set.add(handler as Handler<never>);
    return disposable(() => {
      const current = this.handlers.get(type);
      current?.delete(handler as Handler<never>);
      if (current?.size === 0) this.handlers.delete(type);
    });
  }

  /** Calls every handler. A handler that throws is reported, the rest still run. */
  emit<K extends keyof EventMap>(type: K, payload: EventMap[K]): void {
    const set = this.handlers.get(type);
    if (set === undefined) return;
    for (const handler of Array.from(set) as Handler<EventMap[K]>[]) {
      try {
        handler(payload);
      } catch (error) {
        this.onError(error);
      }
    }
  }

  listenerCount(type: keyof EventMap): number {
    return this.handlers.get(type)?.size ?? 0;
  }
}
