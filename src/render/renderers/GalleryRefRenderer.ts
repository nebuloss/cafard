import { h } from '@/core/Dom.ts';
import type { GalleryItem, GalleryRef } from '@/model/types.ts';
import { sectionLabel } from '@/render/format.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';

/** tpl-gallery-ref (CONTRACTS.md 6.2): the card's link and state on the root. */
export const GALLERY_REF_ATTRIBUTES = ['href', 'data-state', 'data-kind'] as const;

/** An item's title, or "Screenshot <seq>", like pages.ItemTitle. */
export function itemLabel(it: GalleryItem): string {
  return it.title === '' ? `Screenshot ${it.seq}` : it.title;
}

/** "1 image" or "N images", like pages.ImageCount. */
export function imageCount(n: number): string {
  return n === 1 ? '1 image' : `${n} images`;
}

function samePage(href: string | undefined): string | undefined {
  return href !== undefined && href.startsWith('/g/') ? href : undefined;
}

/**
 * A gallery ref in a message, like pages.GalleryRef: an item's card, or a
 * section's strip with its count. A ref the reader may not see says so and
 * nothing else; one retention removed says it is gone.
 */
export class GalleryRefRenderer {
  constructor(private readonly template: SlotTemplate) {}

  render(ref: GalleryRef): HTMLElement {
    const t = this.template.clone();
    if (ref.state === 'unavailable') {
      t.attr({ 'data-state': 'unavailable' }).text('title', 'gallery item unavailable');
      return t.root;
    }
    if (ref.state !== 'ok') {
      t.attr({ 'data-state': 'gone' }).text('title', 'no longer available');
      return t.root;
    }
    const item = ref.item;
    const items = item === undefined ? (ref.items ?? []) : [item];
    t.attr({ 'data-state': 'ok', href: samePage(ref.href), 'data-kind': item === undefined ? 'section' : undefined });
    t.children(
      'thumbs',
      ...items
        .filter((it) => it.image.preview && (it.image.thumb_url ?? '').startsWith('/blob/') && it.image.url.startsWith('/blob/'))
        .map((it) => h('img', { src: it.image.thumb_url, alt: itemLabel(it), 'data-full': it.image.url, loading: 'lazy', decoding: 'async' })),
    );
    if (item !== undefined) {
      t.text('title', itemLabel(item)).text('meta', `#${ref.gallery ?? ''} / ${sectionLabel(item.section ?? '')}`);
    } else {
      t.text('title', sectionLabel(ref.section ?? '')).text('meta', `${imageCount(ref.count ?? 0)} in #${ref.gallery ?? ''}`);
    }
    return t.root;
  }
}
