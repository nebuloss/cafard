import { setAttributes, type AttrValue, type Child, append } from '@/core/Dom.ts';

/**
 * A server-rendered <template id="tpl-..."> (CONTRACTS.md 6.2). The markup
 * of a line, a card or a toast is written once, in Go; the bundle clones it
 * and fills its data-slot parts, so the page and the live view cannot
 * drift and nothing is ever parsed as HTML in the browser.
 */
export class SlotTemplate {
  private constructor(
    private readonly template: HTMLTemplateElement,
    private readonly attributes: ReadonlySet<string>,
  ) {}

  /**
   * The template with this id, or null when the page does not carry it.
   * attributes lists what the client may set on the root (6.2); anything
   * else throws, as a programming error.
   */
  static find(root: Document, id: string, attributes: readonly string[]): SlotTemplate | null {
    const t = root.getElementById(id);
    if (t === null || t.tagName !== 'TEMPLATE') return null;
    return new SlotTemplate(t as HTMLTemplateElement, new Set(attributes));
  }

  /** Like find, but a missing template is an error. */
  static require(root: Document, id: string, attributes: readonly string[]): SlotTemplate {
    const t = SlotTemplate.find(root, id, attributes);
    if (t === null) throw new Error(`the page has no <template id="${id}">`);
    return t;
  }

  clone(): Filled {
    const fragment = this.template.content.cloneNode(true) as DocumentFragment;
    const root = fragment.firstElementChild;
    if (!(root instanceof HTMLElement)) throw new Error('empty template');
    // Adopt into the live document: the template content lives in its own.
    return new Filled(document.adoptNode(root), this.attributes);
  }
}

/** One clone, with typed access to its slots. */
export class Filled {
  private readonly slots = new Map<string, HTMLElement>();

  constructor(
    readonly root: HTMLElement,
    private readonly attributes: ReadonlySet<string>,
  ) {
    for (const el of Array.from(root.querySelectorAll<HTMLElement>('[data-slot]'))) {
      const name = el.dataset['slot'];
      if (name !== undefined && !this.slots.has(name)) this.slots.set(name, el);
    }
  }

  slot(name: string): HTMLElement {
    const el = this.slots.get(name);
    if (el === undefined) throw new Error(`no data-slot="${name}"`);
    return el;
  }

  /** Text slots take textContent only. */
  text(name: string, value: string): this {
    this.slot(name).textContent = value;
    return this;
  }

  /** Container slots take elements (built with h() or other templates). */
  children(name: string, ...children: Child[]): this {
    const el = this.slot(name);
    el.replaceChildren();
    append(el, ...children);
    return this;
  }

  /** Sets attributes on the root, from the template's allowlist. */
  attr(attrs: Readonly<Record<string, AttrValue>>): this {
    for (const name of Object.keys(attrs)) {
      if (!this.attributes.has(name)) throw new Error(`attribute ${name} is not settable on this template`);
    }
    setAttributes(this.root, attrs);
    return this;
  }

  /** Sets an allowlisted attribute on a slot element (href on open). */
  slotAttr(name: string, attrs: Readonly<Record<string, AttrValue>>): this {
    for (const key of Object.keys(attrs)) {
      if (!this.attributes.has(key)) throw new Error(`attribute ${key} is not settable on this template`);
    }
    setAttributes(this.slot(name), attrs);
    return this;
  }
}
