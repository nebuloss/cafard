import { Html } from './html';

/** What a data-act element does when it is used. */
export type Action = (target: HTMLElement, event: Event) => void | Promise<void>;

/**
 * Component is a piece of page that owns one element and draws itself into
 * it from its own state.
 *
 * view() returns the markup; render() puts it in place and calls
 * afterRender(). Behaviour is declared, not wired: an element with
 * data-act="save" runs this.actions.save when clicked, or when Enter or
 * Space is pressed on it if it is a role="button" element that is not a
 * real button. One listener on the root serves every action, so drawing
 * again never leaves stale listeners behind.
 *
 * A component handles the actions it knows and lets the others bubble, so
 * a dialog inside a page handles its own buttons and the page never sees
 * them.
 */
export abstract class Component {
  readonly root: HTMLElement;
  protected actions: Record<string, Action> = {};

  constructor(root: HTMLElement | keyof HTMLElementTagNameMap = 'div', className = '') {
    this.root = typeof root === 'string' ? document.createElement(root) : root;
    if (className) this.root.className = className;
    this.root.addEventListener('click', (e) => this.dispatch(e));
    this.root.addEventListener('keydown', (e) => {
      const t = e.target as HTMLElement;
      if ((e.key === 'Enter' || e.key === ' ') && t.matches('[role="button"][data-act]:not(button)')) {
        e.preventDefault();
        t.click();
      }
    });
  }

  protected abstract view(): Html;

  /** Draws the component again from its state. */
  render(): this {
    this.root.innerHTML = this.view().value;
    this.afterRender();
    return this;
  }

  /** Runs after each render: focus, sizes, child components. */
  protected afterRender(): void {
    Component.sizeBars(this.root);
  }

  private async dispatch(e: Event): Promise<void> {
    const target = (e.target as Element | null)?.closest<HTMLElement>('[data-act]');
    if (!target || !this.root.contains(target)) return;
    const name = target.dataset['act'] ?? '';
    const action = this.actions[name];
    if (!action) return;
    e.stopPropagation();
    if (target.tagName === 'A' || target.tagName === 'BUTTON') e.preventDefault();
    await action(target, e);
  }

  /** The element matching selector inside this component, which must exist. */
  protected $<T extends HTMLElement = HTMLElement>(selector: string): T {
    const el = this.root.querySelector<T>(selector);
    if (!el) throw new Error(`${this.constructor.name}: no ${selector}`);
    return el;
  }

  /** The element matching selector, or null. */
  protected find<T extends HTMLElement = HTMLElement>(selector: string): T | null {
    return this.root.querySelector<T>(selector);
  }

  /**
   * Sets bar widths from data-w. The pages' Content-Security-Policy refuses
   * style attributes in markup; setting element.style from script is
   * allowed, so sizes that depend on data are set here after drawing.
   */
  static sizeBars(root: ParentNode): void {
    root.querySelectorAll<HTMLElement>('[data-w]').forEach((el) => {
      el.style.width = Math.min(100, Math.max(0, Number(el.dataset['w']))).toFixed(1) + '%';
    });
  }
}
