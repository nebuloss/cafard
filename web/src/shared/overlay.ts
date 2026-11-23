import { Component } from './component';

export type LayerStyle = 'right' | 'center';

/**
 * Layer is one level of things drawn over the page: a drawer, or a dialog.
 * Two exist, so the member picker can open over a project's drawer without
 * closing it. Clicking the dimmed backdrop or pressing Escape closes the top
 * one.
 */
export class Layer {
  private static readonly stack: Layer[] = [];
  private content: Component | null = null;
  private onClose: (() => void) | null = null;

  constructor(private readonly host: HTMLElement) {
    host.addEventListener('click', (e) => {
      if (e.target instanceof HTMLElement && e.target.matches('[data-scrim]')) this.close();
    });
  }

  get isOpen(): boolean {
    return this.content !== null;
  }

  /** Shows content in this layer, replacing what was there. */
  open(content: Component, style: LayerStyle, onClose?: () => void): void {
    this.closeQuietly();
    this.content = content;
    this.onClose = onClose ?? null;
    const scrim = document.createElement('div');
    scrim.className = `scrim ${style}`;
    scrim.dataset['scrim'] = '';
    content.root.classList.add(style === 'right' ? 'drawer' : 'modal');
    content.root.setAttribute('role', 'dialog');
    content.root.setAttribute('aria-modal', 'true');
    scrim.append(content.root);
    this.host.replaceChildren(scrim);
    content.render();
    Layer.stack.push(this);
  }

  close(): void {
    const cb = this.onClose;
    this.closeQuietly();
    cb?.();
  }

  private closeQuietly(): void {
    if (!this.content) return;
    this.host.replaceChildren();
    this.content = null;
    this.onClose = null;
    const i = Layer.stack.indexOf(this);
    if (i >= 0) Layer.stack.splice(i, 1);
  }

  /** Closes the top layer; for Escape. */
  static closeTop(): boolean {
    const top = Layer.stack[Layer.stack.length - 1];
    if (!top) return false;
    top.close();
    return true;
  }
}

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && Layer.closeTop()) e.preventDefault();
});
