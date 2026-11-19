import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import { GalleryView } from '@/components/GalleryView.ts';
import { ImageViewer } from '@/components/ImageViewer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { GALLERY_ITEM_ATTRIBUTES, GalleryItemRenderer } from '@/render/renderers/GalleryItemRenderer.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell } from '@/pages/Shell.ts';

/**
 * A gallery (PLAN 9.6.3, "Galleries"): the grid the server rendered, the
 * viewer on a tap, and on the newest page the gallery's new items live,
 * from the feed of its channel.
 */
export class GalleryPage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const { doc, services } = this;
    const root = Dom.find('[data-component="GalleryView"]', doc);
    const chat = root?.dataset['chat'] ?? '';
    const shell = mountShell(doc, services, bag, `/api/events?chat=${encodeURIComponent(chat)}`);
    const viewerEl = Dom.find<HTMLDialogElement>('[data-component="ImageViewer"]', doc);
    const viewer = viewerEl === null ? null : new ImageViewer(viewerEl);
    if (viewer !== null) bag.add(viewer.mount());
    if (root === null) return bag;
    const tpl = SlotTemplate.find(doc, 'tpl-gallery-item', GALLERY_ITEM_ATTRIBUTES);
    const gallery = new GalleryView(root, tpl === null ? null : new GalleryItemRenderer(tpl), (images, index) => viewer?.open(images, index));
    bag.add(gallery.mount());
    const live = root.hasAttribute('data-live');
    if (live) bag.add(shell.feed.events.on('entry', (e) => void gallery.add(e)));
    bag.add(shell.feed.start(live ? (Dom.number(root, 'data-next-seq') ?? 0) : 0));
    return bag;
  }
}
