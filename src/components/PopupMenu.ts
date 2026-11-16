import { Dom, h } from '@/core/Dom.ts';

/** A menu entry: a button that runs, or a link. */
export type MenuItem = { readonly label: string; readonly run: () => void } | { readonly label: string; readonly href: string };

/**
 * Opens a popup menu (class "menu <kind>") right after anchor, closing the
 * one of that kind already open in scope. A button closes the menu, then runs.
 */
export function popupMenu(scope: ParentNode, anchor: Element, kind: string, items: readonly (MenuItem | null)[]): HTMLElement {
  Dom.find(`.${kind}`, scope)?.remove();
  const popup = h('div', { class: `menu ${kind}`, role: 'menu' });
  for (const item of items) {
    if (item === null) continue;
    if ('href' in item) {
      popup.append(h('a', { class: 'menu-item', role: 'menuitem', href: item.href }, item.label));
      continue;
    }
    const b = h('button', { class: 'menu-item', type: 'button', role: 'menuitem' }, item.label);
    b.addEventListener('click', () => {
      popup.remove();
      item.run();
    });
    popup.append(b);
  }
  anchor.after(popup);
  popup.querySelector('button')?.focus();
  return popup;
}
