import { Component } from './component';

/**
 * Where a layer's content sits: a drawer on the right, a dialog in the
 * middle, a sheet over the whole screen, one rising from the bottom, or the
 * dark full-screen image viewer.
 */
export type LayerStyle = 'right' | 'center' | 'full' | 'bottom' | 'lightbox';

// Where each style puts the layer, and what its content looks like: MD3's
// dialog and side sheet, on a dimmed backdrop.
const SCRIM: Record<LayerStyle, string> = {
  right: 'fixed inset-0 z-40 flex justify-end bg-black/45',
  center: 'fixed inset-0 z-40 flex items-center justify-center bg-black/45 sm:p-4',
  full: 'fixed inset-0 z-40 flex bg-black/45',
  bottom: 'fixed inset-0 z-40 flex items-end bg-black/45',
  lightbox: 'fixed inset-0 z-40 flex bg-black/90',
};

const DEFAULT_CLASS: Record<LayerStyle, string> = {
  right: 'flex h-full w-full max-w-[460px] flex-col bg-surface animate-sheet-in',
  center: 'flex h-full w-full flex-col overflow-hidden bg-surface sm:h-auto sm:max-h-full sm:max-w-[620px] sm:rounded-md3-xl sm:[animation:pop_200ms_cubic-bezier(.2,0,0,1)]',
  full: 'flex h-full w-full flex-col bg-surface',
  bottom: 'flex max-h-[90%] w-full flex-col rounded-t-md3-xl bg-surface',
  lightbox: 'flex h-full w-full flex-col',
};

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

  /**
   * Shows content in this layer, replacing what was there. className is the
   * class its content gets, by default the one its style calls for.
   */
  open(content: Component, style: LayerStyle, onClose?: () => void, className?: string): void {
    this.closeQuietly();
    this.content = content;
    this.onClose = onClose ?? null;
    const scrim = document.createElement('div');
    scrim.className = SCRIM[style];
    scrim.dataset['scrim'] = '';
    content.root.className = `${content.root.className} ${className ?? DEFAULT_CLASS[style]}`.trim();
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
