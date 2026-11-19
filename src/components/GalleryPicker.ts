import { Dom, h } from '@/core/Dom.ts';
import { Component } from '@/components/Component.ts';
import { closeDialog, openDialog } from '@/components/ImageViewer.ts';
import type { GalleryPage } from '@/model/types.ts';
import type { CafardApi } from '@/net/CafardApi.ts';
import { ApiError } from '@/net/ApiError.ts';
import { sectionLabel, sectionRef } from '@/render/format.ts';
import { itemLabel } from '@/render/renderers/GalleryRefRenderer.ts';

/**
 * "Attach from gallery" (PLAN 6.4): the project's galleries, a gallery's
 * sections and images. Picking an image, or a whole section, hands its ref
 * to pick (the composer writes it in the message); nothing is copied.
 */
export class GalleryPicker extends Component<HTMLDialogElement> {
  private gallery = '';

  constructor(
    root: HTMLDialogElement,
    private readonly api: CafardApi,
    private readonly pick: (ref: string) => void,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.listen(this.root, 'click', (event) => {
      const el = (event.target as Element | null)?.closest<HTMLElement>('button');
      if (el === null || el === undefined) return;
      if (el.dataset['action'] === 'close') closeDialog(this.root);
      const gallery = el.dataset['gallery'];
      if (gallery !== undefined) void this.show(gallery);
      const section = el.dataset['section'];
      if (section !== undefined) void this.show(this.gallery, section === '' ? undefined : section);
      const ref = el.dataset['ref'];
      if (ref !== undefined) {
        this.pick(ref);
        closeDialog(this.root);
      }
    });
  }

  open(): void {
    openDialog(this.root);
    const galleries = Dom.findAll<HTMLButtonElement>('[data-slot="galleries"] button[data-gallery]', this.root);
    const only = galleries.length === 1 ? galleries[0]?.dataset['gallery'] : undefined;
    if (only !== undefined) void this.show(only);
  }

  /** Loads a gallery, on one section when section is given. */
  async show(gallery: string, section?: string): Promise<void> {
    this.gallery = gallery;
    const status = Dom.find('[data-slot="status"]', this.root);
    Dom.setText(status, 'Loading...');
    for (const b of Dom.findAll<HTMLButtonElement>('[data-slot="galleries"] button', this.root)) b.setAttribute('aria-pressed', String(b.dataset['gallery'] === gallery));
    let page: GalleryPage;
    try {
      page = await this.api.gallery(gallery, section === undefined ? {} : { section });
    } catch (e) {
      Dom.setText(status, e instanceof ApiError ? e.message : 'The gallery could not be loaded.');
      return;
    }
    const sections = Dom.find('[data-slot="sections"]', this.root);
    sections?.replaceChildren(
      h('button', { class: 'chip filter', type: 'button', 'data-section': '', 'aria-pressed': String(section === undefined) }, 'All'),
      ...page.sections
        .filter((s) => s.name !== '')
        .map((s) => h('button', { class: 'chip filter', type: 'button', 'data-section': s.name, 'aria-pressed': String(s.name === section) }, `${s.name} (${s.items})`)),
      ...(section === undefined ? [] : [h('button', { class: 'btn tonal', type: 'button', 'data-ref': sectionRef(gallery, section) }, `Attach the section ${sectionLabel(section)}`)]),
    );
    Dom.find('[data-slot="items"]', this.root)?.replaceChildren(
      ...page.items.map((it) => {
        const thumb = it.image.preview && (it.image.thumb_url ?? '').startsWith('/blob/') ? it.image.thumb_url : undefined;
        return h(
          'button',
          { class: 'gallery-item gallery-pick', type: 'button', 'data-ref': it.ref, 'data-seq': it.seq },
          thumb === undefined ? h('span', { class: 'image-file' }, 'HEIC') : h('img', { src: thumb, alt: itemLabel(it), loading: 'lazy', decoding: 'async' }),
          h('span', { class: 'gallery-title' }, itemLabel(it)),
        );
      }),
    );
    Dom.setText(status, page.items.length === 0 ? 'No image here yet.' : 'Pick an image to show it in your message.');
  }
}
