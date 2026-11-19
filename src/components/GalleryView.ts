import { Dom, h } from '@/core/Dom.ts';
import { Component } from '@/components/Component.ts';
import type { ViewerImage } from '@/components/ImageViewer.ts';
import type { Entry } from '@/model/types.ts';
import { sectionLabel } from '@/render/format.ts';
import { itemImage, type GalleryItemRenderer } from '@/render/renderers/GalleryItemRenderer.ts';

/**
 * A gallery's grid (PLAN 9.6.3, "Galleries"). A tap on a tile opens the
 * viewer on the tiles of its section, so previous and next stay in the
 * section; new items of the gallery arrive from the feed and go first in
 * their section, which moves to the top. Without the bundle every tile is
 * a link to the item's page.
 */
export class GalleryView extends Component {
  private readonly chat: string;
  /** The section the page is filtered on, or null for all. */
  private readonly filter: string | null;

  constructor(
    root: HTMLElement,
    private readonly tiles: GalleryItemRenderer | null,
    private readonly view: (images: readonly ViewerImage[], index: number) => void,
  ) {
    super(root);
    this.chat = root.dataset['chat'] ?? '';
    this.filter = root.hasAttribute('data-filtered') ? (root.dataset['section'] ?? '') : null;
  }

  protected bind(): void {
    this.bag.listen(this.root, 'click', (event) => {
      const e = event as MouseEvent;
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const tile = (e.target as Element | null)?.closest<HTMLAnchorElement>('a.gallery-item');
      const group = tile?.closest('.gallery-group');
      if (tile === null || tile === undefined || group === null || group === undefined) return;
      const all = Dom.findAll<HTMLAnchorElement>('a.gallery-item[data-full]', group);
      const index = all.indexOf(tile);
      if (index < 0) return;
      e.preventDefault();
      this.view(all.map((a) => ({ src: a.dataset['full'] ?? '', alt: a.querySelector('img')?.alt ?? '', caption: a.querySelector('[data-slot="title"]')?.textContent ?? '' })), index);
    });
  }

  /** Adds a message of the feed when it is a new item of this gallery; returns its tile. */
  add(e: Entry): HTMLElement | null {
    if (e.kind !== 'message' || e.chat !== this.chat || this.tiles === null) return null;
    const img = itemImage(e);
    if (img === null) return null;
    const section = e.section ?? '';
    if (this.filter !== null && this.filter !== section) return null;
    if (this.root.querySelector(`a.gallery-item[data-seq="${e.seq}"]`) !== null) return null;
    const groups = Dom.find('[data-slot="groups"]', this.root);
    if (groups === null) return null;
    let group = Dom.findAll('.gallery-group', groups).find((g) => (g.dataset['section'] ?? '') === section) ?? null;
    if (group === null) {
      group = h('section', { class: 'gallery-group', 'data-section': section }, h('h3', { class: 'list-heading' }, sectionLabel(section)), h('div', { class: 'gallery-grid' }));
    }
    // The section with the newest image comes first.
    if (groups.firstElementChild !== group) groups.prepend(group);
    const tile = this.tiles.render(e, img);
    Dom.find('.gallery-grid', group)?.prepend(tile);
    Dom.find('[data-slot="empty"]', this.root)?.remove();
    return tile;
  }
}
