import { Dom } from '@/core/Dom.ts';
import { Component } from '@/components/Component.ts';

export interface ViewerImage {
  readonly src: string;
  readonly alt: string;
}

/** Shows a dialog, where the browser has showModal. */
export function openDialog(dialog: HTMLDialogElement): void {
  if (typeof dialog.showModal === 'function') {
    if (!dialog.open) dialog.showModal();
  } else {
    dialog.setAttribute('open', '');
  }
}

export function closeDialog(dialog: HTMLDialogElement): void {
  if (typeof dialog.close === 'function') dialog.close();
  else dialog.removeAttribute('open');
}

/**
 * The image viewer (PLAN 9.6.3): the original, native pinch zoom (the
 * image is plain), previous and next with buttons, arrows or a swipe.
 * Esc closes it, as any dialog.
 */
export class ImageViewer extends Component<HTMLDialogElement> {
  private images: readonly ViewerImage[] = [];
  private index = 0;
  private startX: number | null = null;

  protected bind(): void {
    this.bag.listen(this.root, 'click', (event) => {
      const action = (event.target as Element | null)?.closest<HTMLElement>('[data-action]')?.dataset['action'];
      if (action === 'close') closeDialog(this.root);
      if (action === 'prev') this.show(this.index - 1);
      if (action === 'next') this.show(this.index + 1);
    });
    this.bag.listen(this.root, 'keydown', (event) => {
      const key = (event as KeyboardEvent).key;
      if (key === 'ArrowLeft') this.show(this.index - 1);
      if (key === 'ArrowRight') this.show(this.index + 1);
    });
    this.bag.listen(this.root, 'pointerdown', (event) => {
      this.startX = (event as PointerEvent).clientX;
    });
    this.bag.listen(this.root, 'pointerup', (event) => {
      if (this.startX === null) return;
      const dx = (event as PointerEvent).clientX - this.startX;
      this.startX = null;
      if (Math.abs(dx) > 60) this.show(this.index + (dx < 0 ? 1 : -1));
    });
  }

  open(images: readonly ViewerImage[], index: number): void {
    this.images = images;
    this.show(index);
    openDialog(this.root);
  }

  private show(index: number): void {
    if (this.images.length === 0) return;
    this.index = (index + this.images.length) % this.images.length;
    const img = this.images[this.index];
    if (img === undefined) return;
    const el = Dom.find<HTMLImageElement>('[data-slot="img"]', this.root);
    if (el !== null && img.src.startsWith('/blob/')) {
      el.src = img.src;
      el.alt = img.alt;
    }
    const open = Dom.find<HTMLAnchorElement>('[data-slot="open"]', this.root);
    if (open !== null && img.src.startsWith('/blob/')) open.href = img.src;
    for (const b of Dom.findAll<HTMLButtonElement>('[data-action="prev"], [data-action="next"]', this.root)) b.hidden = this.images.length < 2;
  }
}
