export type AttrValue = string | number | boolean | null | undefined;
export type Child = Node | string | null | undefined | false;

const URL_ATTRIBUTES = new Set(['href', 'src', 'action', 'formaction', 'poster', 'xlink:href']);
const FORBIDDEN_ATTRIBUTES = new Set(['srcdoc', 'style']);

/**
 * Whether a URL may go into an href or src: a same-origin path, a fragment,
 * a query, or http(s). Everything else, javascript: and data: included, is
 * refused.
 */
export function isSafeUrl(url: string): boolean {
  const value = url.trim();
  if (value.startsWith('#') || value.startsWith('?')) return true;
  if (value.startsWith('/')) return !value.startsWith('//') && !value.startsWith('/\\');
  return /^https?:\/\//i.test(value);
}

/**
 * Builds an element. Text goes in through text nodes only, so a string from
 * the network can never become markup. Event handler attributes, inline
 * styles and unsafe URLs throw: they are programming errors, not input.
 */
export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Readonly<Record<string, AttrValue>> = {},
  ...children: Child[]
): HTMLElementTagNameMap[K] {
  const element = document.createElement(tag);
  setAttributes(element, attrs);
  append(element, ...children);
  return element;
}

export function setAttributes(element: Element, attrs: Readonly<Record<string, AttrValue>>): void {
  for (const [name, value] of Object.entries(attrs)) {
    const lower = name.toLowerCase();
    if (lower.startsWith('on') || FORBIDDEN_ATTRIBUTES.has(lower)) {
      throw new Error(`refusing attribute ${name}`);
    }
    if (value === false || value === null || value === undefined) {
      element.removeAttribute(name);
      continue;
    }
    const text = value === true ? '' : String(value);
    if (URL_ATTRIBUTES.has(lower) && !isSafeUrl(text)) {
      throw new Error(`refusing ${name}=${JSON.stringify(text)}`);
    }
    element.setAttribute(name, text);
  }
}

export function append(parent: Node, ...children: Child[]): void {
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    parent.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
  }
}

/**
 * Typed lookups. A miss returns null: the server renders whole pages and the
 * bundle only enhances them, so a missing element means the feature is not on
 * this page.
 */
export class Dom {
  static find<E extends Element = HTMLElement>(selector: string, root: ParentNode = document): E | null {
    return root.querySelector<E>(selector);
  }

  static findAll<E extends Element = HTMLElement>(selector: string, root: ParentNode = document): E[] {
    return Array.from(root.querySelectorAll<E>(selector));
  }

  /** A number from a data attribute, or null when missing or not a number. */
  static number(element: Element, attribute: string): number | null {
    const raw = element.getAttribute(attribute);
    if (raw === null || raw.trim() === '') return null;
    const value = Number(raw);
    return Number.isFinite(value) ? value : null;
  }

  static setText(element: Element | null, text: string): void {
    if (element !== null && element.textContent !== text) element.textContent = text;
  }

  static clear(element: Element): void {
    element.replaceChildren();
  }
}
