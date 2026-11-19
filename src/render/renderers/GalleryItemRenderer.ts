import type { Entry, ImageAttachment } from '@/model/types.ts';
import { itemHref, itemTitle } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';

/** tpl-gallery-item (CONTRACTS.md 6.2): the tile's link, its original, and the image slot. */
export const GALLERY_ITEM_ATTRIBUTES = ['href', 'data-seq', 'data-full', 'src', 'alt', 'width', 'height'] as const;

/** The image of a gallery message: its first, as messaging.galleryItem takes it. */
export function itemImage(e: Entry): ImageAttachment | null {
  for (const a of e.attachments ?? []) if (a.type === 'image') return a;
  return null;
}

function blobOnly(url: string | undefined): string | undefined {
  return url !== undefined && url.startsWith('/blob/') ? url : undefined;
}

/** A gallery tile from a message, like pages.GalleryItem. */
export class GalleryItemRenderer {
  constructor(private readonly template: SlotTemplate) {}

  render(e: Entry, img: ImageAttachment): HTMLElement {
    const t = this.template.clone();
    const gallery = (e.chat ?? '').slice(5);
    const title = itemTitle(e.text);
    t.attr({ href: itemHref(gallery, e.seq), 'data-seq': e.seq, 'data-full': blobOnly(img.url) });
    const src = img.preview ? blobOnly(img.thumb_url) : undefined;
    if (src === undefined) {
      const file = document.createElement('span');
      file.className = 'image-file';
      file.dataset['slot'] = 'img';
      file.textContent = 'HEIC: open to download';
      t.slot('img').replaceWith(file);
    } else {
      t.slotAttr('img', { src, alt: title === '' ? `Screenshot ${e.seq}` : title, width: img.width, height: img.height });
    }
    t.text('title', title);
    return t.root;
  }
}
