import { DisposableBag, disposable, type Disposable } from '@/core/Disposable.ts';

/**
 * The one base class (PLAN 9.4 rule 6), a Template Method: mount() calls
 * build() then bind(), and the returned Disposable releases everything bind
 * registered in the bag. Subclasses implement build and bind; they never
 * override mount or unmount.
 */
export abstract class Component<E extends HTMLElement = HTMLElement> {
  protected readonly bag = new DisposableBag();
  private mounted = false;

  constructor(protected readonly root: E) {}

  mount(): Disposable {
    if (this.mounted) throw new Error(`${this.constructor.name} mounted twice`);
    this.mounted = true;
    this.build();
    this.bind();
    return disposable(() => this.unmount());
  }

  unmount(): void {
    this.bag.dispose();
  }

  /** Finds or creates the elements the component needs. Optional. */
  protected build(): void {}

  /** Attaches listeners and subscriptions, each in this.bag. */
  protected abstract bind(): void;
}
