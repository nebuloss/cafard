import type { ImageAttachment } from '@/model/types.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';

/** tpl-image (CONTRACTS.md 6.2), plus href on the root: the original, /blob/ only. */
export const IMAGE_ATTRIBUTES = ['src', 'alt', 'width', 'height', 'data-sha', 'href'] as const;

/** How many tiles a grid shows before "+N", like pages.maxTiles. */
export const MAX_TILES = 4;

function blobOnly(url: string | undefined): string | undefined {
  return url !== undefined && url.startsWith('/blob/') ? url : undefined;
}

/** One image tile; HEIC (no preview) keeps the file tile's text. */
export class ImageRenderer {
  constructor(private readonly template: SlotTemplate) {}

  render(img: ImageAttachment, more = 0): HTMLElement {
    const t = this.template.clone();
    t.attr({ 'data-sha': img.sha256, href: blobOnly(img.url) });
    const src = img.preview ? blobOnly(img.thumb_url) : undefined;
    if (src === undefined) {
      const file = document.createElement('span');
      file.className = 'image-file';
      file.dataset['slot'] = 'img';
      file.textContent = 'HEIC photo: open to download';
      t.slot('img').replaceWith(file);
    } else {
      t.slotAttr('img', { src, alt: 'Photo', width: img.width, height: img.height });
    }
    t.text('more', more > 0 ? `+${more}` : '');
    return t.root;
  }

  /** The grid of a message: at most MAX_TILES, the last one saying how many more. */
  grid(images: readonly ImageAttachment[]): HTMLElement | null {
    if (images.length === 0) return null;
    const grid = document.createElement('div');
    grid.className = 'images';
    const shown = images.slice(0, MAX_TILES);
    grid.dataset['count'] = String(shown.length);
    shown.forEach((img, i) => grid.append(this.render(img, i === MAX_TILES - 1 ? images.length - MAX_TILES : 0)));
    return grid;
  }
}
