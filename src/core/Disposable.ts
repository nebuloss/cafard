/** Something that holds a resource until disposed. */
export interface Disposable {
  dispose(): void;
}

/** Wraps a cleanup function. Calling dispose twice runs it once. */
export function disposable(cleanup: () => void): Disposable {
  let done = false;
  return {
    dispose(): void {
      if (done) return;
      done = true;
      cleanup();
    },
  };
}

/**
 * Every subscription, listener and timer a component owns, released in one
 * call. Leaked EventSource connections are the bug this exists to prevent.
 */
export class DisposableBag implements Disposable {
  private readonly items: Disposable[] = [];
  private disposed = false;

  get isDisposed(): boolean {
    return this.disposed;
  }

  /** Adds an item. Adding to a disposed bag disposes the item at once. */
  add<D extends Disposable>(item: D): D {
    if (this.disposed) {
      item.dispose();
    } else {
      this.items.push(item);
    }
    return item;
  }

  /** Adds a cleanup function. */
  defer(cleanup: () => void): Disposable {
    return this.add(disposable(cleanup));
  }

  /** Adds an event listener and keeps its removal. */
  listen<K extends keyof HTMLElementEventMap>(
    target: HTMLElement,
    type: K,
    handler: (event: HTMLElementEventMap[K]) => void,
    options?: AddEventListenerOptions,
  ): Disposable;
  listen(target: EventTarget, type: string, handler: (event: Event) => void, options?: AddEventListenerOptions): Disposable;
  listen(target: EventTarget, type: string, handler: (event: Event) => void, options?: AddEventListenerOptions): Disposable {
    target.addEventListener(type, handler, options);
    return this.defer(() => target.removeEventListener(type, handler, options));
  }

  /** Releases everything, newest first. One failing item does not stop the others. */
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    const errors: unknown[] = [];
    while (this.items.length > 0) {
      const item = this.items.pop();
      try {
        item?.dispose();
      } catch (error) {
        errors.push(error);
      }
    }
    if (errors.length === 1) throw errors[0];
    if (errors.length > 1) throw new AggregateError(errors, 'several disposals failed');
  }
}
