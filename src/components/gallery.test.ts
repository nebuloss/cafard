import { afterEach, describe, expect, it } from 'vitest';
import type { Disposable } from '@/core/Disposable.ts';
import type { Attachment, Entry } from '@/model/types.ts';
import { GalleryPage } from '@/pages/GalleryPage.ts';
import { chatPageHref, galleryHref, itemHref, itemTitle } from '@/render/format.ts';
import { fixture, loadPage } from '@/testing/pages.ts';
import { fakeServices } from '@/testing/services.ts';

const message = fixture('entry_gallery_item') as Entry;

function image(sha: string): Attachment {
  return { type: 'image', sha256: sha, mime: 'image/png', width: 1280, height: 800, url: `/blob/${sha}`, thumb_url: `/blob/${sha}?v=thumb`, preview: true };
}

function item(seq: number, title: string, section: string | undefined, sha: string, chat = 'chan:lab/shots'): Entry {
  const { section: _drop, ...rest } = message;
  return { ...rest, seq, chat, stream: chat, text: title, attachments: [image(sha.repeat(64))], ...(section === undefined ? {} : { section }) } as Entry;
}

let running: Disposable | null = null;
afterEach(() => {
  running?.dispose();
  running = null;
});

function start(): ReturnType<typeof fakeServices> {
  loadPage('gallery');
  const fake = fakeServices(() => message);
  running = new GalleryPage(document, fake.services).start();
  return fake;
}

const groups = (): (string | undefined)[] => Array.from(document.querySelectorAll<HTMLElement>('.gallery-group')).map((g) => g.dataset['section']);
function titles(section: string): string[] {
  const group = Array.from(document.querySelectorAll<HTMLElement>('.gallery-group')).find((g) => g.dataset['section'] === section);
  return Array.from(group?.querySelectorAll('[data-slot="title"]') ?? []).map((t) => t.textContent ?? '');
}

describe('gallery strings', () => {
  it('match the Go ones', () => {
    expect(galleryHref('lab/shots')).toBe('/projects/lab/g/shots');
    expect(galleryHref('lab/shots', 'login flow')).toBe('/projects/lab/g/shots?section=login+flow');
    expect(galleryHref('lab/shots', '')).toBe('/projects/lab/g/shots?section=');
    expect(itemHref('lab/shots', 42)).toBe('/projects/lab/g/shots/42');
    expect(itemTitle('  after submit\nthe rest ')).toBe('after submit ...');
    expect(itemTitle('é'.repeat(141))).toBe(`${'é'.repeat(140)}...`);
    expect(chatPageHref({ id: 'chan:lab/shots', channel_kind: 'gallery' })).toBe('/projects/lab/g/shots');
    expect(chatPageHref({ id: 'chan:lab/dut1', channel_kind: 'chat' })).toBe('/projects/lab/c/dut1');
  });
});

describe('the gallery page', () => {
  it('groups the server-rendered items by section, newest first', () => {
    start();
    expect(groups()).toEqual(['login flow', '']);
    expect(titles('login flow')).toEqual(['after submit ...', 'login page']);
    expect(document.querySelector('.gallery-group[data-section=""] .list-heading')?.textContent).toBe('Unsorted');
  });

  it('opens the viewer on a tile, previous and next staying in its section', () => {
    start();
    const tiles = Array.from(document.querySelectorAll<HTMLAnchorElement>('.gallery-group[data-section="login flow"] a.gallery-item'));
    const click = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    tiles[1]?.querySelector('img')?.dispatchEvent(click);
    expect(click.defaultPrevented).toBe(true);
    const viewer = document.querySelector<HTMLDialogElement>('dialog.image-viewer');
    const shown = (): string | null | undefined => viewer?.querySelector('[data-slot="img"]')?.getAttribute('src');
    const caption = (): string | null | undefined => viewer?.querySelector('[data-slot="caption"]')?.textContent;
    expect(viewer?.open).toBe(true);
    expect(shown()).toBe(tiles[1]?.dataset['full']);
    expect(caption()).toBe('login page');
    const next = viewer?.querySelector<HTMLButtonElement>('[data-action="next"]');
    expect(next?.hidden).toBe(false);
    next?.click();
    expect(shown()).toBe(tiles[0]?.dataset['full']);
    expect(caption()).toBe('after submit ...');
    // Two items in the section: next comes back round, never to Unsorted.
    next?.click();
    expect(shown()).toBe(tiles[1]?.dataset['full']);
    viewer?.querySelector<HTMLButtonElement>('[data-action="prev"]')?.click();
    expect(shown()).toBe(tiles[0]?.dataset['full']);

    // An item alone in its section has no previous or next.
    document.querySelector<HTMLElement>('.gallery-group[data-section=""] a.gallery-item img')?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
    expect(caption()).toBe('boot splash');
    expect(next?.hidden).toBe(true);
  });

  it('leaves a modified click to the browser', () => {
    start();
    const click = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, ctrlKey: true });
    document.querySelector('a.gallery-item img')?.dispatchEvent(click);
    expect(click.defaultPrevented).toBe(false);
  });

  it('adds new items live, first in their section, which moves to the top', () => {
    const { sources } = start();
    expect(sources[0]?.url).toContain('chat=chan%3Alab%2Fshots');
    const next = document.querySelector<HTMLElement>('[data-component="GalleryView"]')?.dataset['nextSeq'];
    expect(next).toMatch(/^[1-9][0-9]*$/);
    expect(sources[0]?.url).toContain(`since_seq=${next ?? ''}`);
    sources[0]?.emit('entry', item(20, 'splash v2', undefined, 'c'));
    expect(groups()).toEqual(['', 'login flow']);
    expect(titles('')).toEqual(['splash v2', 'boot splash']);
    const tile = document.querySelector<HTMLAnchorElement>('a.gallery-item[data-seq="20"]');
    expect(tile?.getAttribute('href')).toBe('/projects/lab/g/shots/20');
    expect(tile?.dataset['full']).toBe(`/blob/${'c'.repeat(64)}`);
    expect(tile?.querySelector('img')?.getAttribute('src')).toBe(`/blob/${'c'.repeat(64)}?v=thumb`);

    sources[0]?.emit('entry', item(21, 'sign-up form', 'sign-up', 'd'));
    expect(groups()).toEqual(['sign-up', '', 'login flow']);
    expect(document.querySelector('.gallery-group[data-section="sign-up"] .list-heading')?.textContent).toBe('sign-up');

    // Twice the same entry, another chat's, a marker: nothing more.
    sources[0]?.emit('entry', item(21, 'sign-up form', 'sign-up', 'd'));
    sources[0]?.emit('entry', item(22, 'elsewhere', 'sign-up', 'e', 'chan:lab/dut1'));
    sources[0]?.emit('entry', { ...item(23, 'kind changed', undefined, 'f'), kind: 'marker', attachments: [] } as Entry);
    expect(document.querySelectorAll('a.gallery-item')).toHaveLength(5);

    // A new tile opens the viewer like the others.
    document.querySelector<HTMLElement>('a.gallery-item[data-seq="20"] img')?.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
    expect(document.querySelector('dialog.image-viewer [data-slot="caption"]')?.textContent).toBe('splash v2');
  });
});
