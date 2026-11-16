import { disposable, type Disposable } from '@/core/Disposable.ts';

type Listener<T> = (value: T, previous: T) => void;

/** A value that tells its subscribers when it changes. */
export class Observable<T> {
  private current: T;
  private readonly listeners = new Set<Listener<T>>();

  constructor(
    initial: T,
    private readonly equals: (a: T, b: T) => boolean = Object.is,
  ) {
    this.current = initial;
  }

  get value(): T {
    return this.current;
  }

  /** Replaces the value. Subscribers run only if it differs. */
  set(next: T): void {
    const previous = this.current;
    if (this.equals(previous, next)) return;
    this.current = next;
    for (const listener of Array.from(this.listeners)) {
      try {
        listener(next, previous);
      } catch (error) {
        console.error('cafard:', error);
      }
    }
  }

  update(change: (value: T) => T): void {
    this.set(change(this.current));
  }

  /** With immediate, the listener also runs now with the current value. */
  subscribe(listener: Listener<T>, options: { immediate?: boolean } = {}): Disposable {
    this.listeners.add(listener);
    if (options.immediate === true) listener(this.current, this.current);
    return disposable(() => this.listeners.delete(listener));
  }
}
