import { afterEach, describe, expect, it } from 'vitest';
import type { Disposable } from '@/core/Disposable.ts';
import type { Entry, GalleryPage } from '@/model/types.ts';
import { ChatPage } from '@/pages/ChatPage.ts';
import { galleryImagesLabel } from '@/render/format.ts';
import { FakeFrames } from '@/testing/FakeFrames.ts';
import { fixture, loadPage } from '@/testing/pages.ts';
import { fakeServices, flush } from '@/testing/services.ts';

const message = fixture('entry_message') as Entry;
const refs = fixture('entry_gallery_refs') as Entry;
const page = fixture('gallery_page') as GalleryPage;

let running: Disposable | null = null;
afterEach(() => {
  running?.dispose();
  running = null;
});

function start(answer?: (method: string, path: string) => unknown): ReturnType<typeof fakeServices> & { frames: FakeFrames } {
  loadPage('chat');
  const fake = fakeServices((job) => ({ ...message, seq: 900, chat: job.chat, text: job.text }), answer);
  const frames = new FakeFrames();
  running = new ChatPage(document, fake.services, frames).start();
  return { ...fake, frames };
}

const click = (el: Element | null | undefined): MouseEvent => {
  const ev = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
  el?.dispatchEvent(ev);
  return ev;
};

describe('gallery refs in a chat', () => {
  it('come from the server as cards that work without the bundle', () => {
    loadPage('chat');
    const cards = Array.from(document.querySelectorAll<HTMLAnchorElement>('.conversation a.gallery-ref'));
    expect(cards.map((c) => c.dataset['state'])).toEqual(['ok', 'ok']);
    expect(cards[0]?.getAttribute('href')).toMatch(/^\/projects\/lab\/g\/shots\/\d+$/);
    expect(cards[0]?.querySelector('[data-slot="meta"]')?.textContent).toBe('#lab/shots / Unsorted');
    expect(cards[1]?.dataset['kind']).toBe('section');
    expect(cards[1]?.querySelector('[data-slot="meta"]')?.textContent).toBe('2 images in #lab/shots');
  });

  it('render live like the server: item, section, unavailable and gone', () => {
    const { sources, frames } = start();
    sources[0]?.emit('entry', refs);
    frames.run();
    const row = document.querySelector(`[data-component="ConversationView"] [data-seq="${refs.seq}"]`);
    const cards = Array.from(row?.querySelectorAll<HTMLAnchorElement>('a.gallery-ref') ?? []);
    expect(cards.map((c) => c.dataset['state'])).toEqual(['ok', 'ok', 'unavailable', 'gone']);
    const [item, section, hidden, gone] = cards;
    expect(item?.getAttribute('href')).toBe('/projects/lab/g/screens/48433');
    expect(item?.querySelector('img')?.getAttribute('src')).toBe(page.items[0]?.image.thumb_url);
    expect(item?.querySelector('[data-slot="title"]')?.textContent).toBe('login page after the fix');
    expect(item?.querySelector('[data-slot="meta"]')?.textContent).toBe('#lab/screens / login flow v2');
    expect(section?.dataset['kind']).toBe('section');
    expect(section?.getAttribute('href')).toBe('/projects/lab/g/screens?section=login+flow+v2');
    expect(section?.querySelector('[data-slot="title"]')?.textContent).toBe('login flow v2');
    expect(section?.querySelector('[data-slot="meta"]')?.textContent).toBe('2 images in #lab/screens');
    // Nothing but the word: no link, no thumbnail, no name.
    expect(hidden?.hasAttribute('href')).toBe(false);
    expect(hidden?.querySelectorAll('img')).toHaveLength(0);
    expect(hidden?.textContent).toBe('gallery item unavailable');
    expect(gone?.hasAttribute('href')).toBe(false);
    expect(gone?.textContent).toBe('no longer available');
  });

  it('open the viewer on a tap', () => {
    const { sources, frames } = start();
    sources[0]?.emit('entry', refs);
    frames.run();
    const card = document.querySelector(`[data-seq="${refs.seq}"] a.gallery-ref[data-kind="section"]`);
    expect(click(card?.querySelector('img')).defaultPrevented).toBe(true);
    const viewer = document.querySelector<HTMLDialogElement>('dialog.image-viewer');
    expect(viewer?.open).toBe(true);
    expect(viewer?.querySelector('[data-slot="img"]')?.getAttribute('src')).toBe(page.items[0]?.image.url);
    expect(viewer?.querySelector('[data-slot="caption"]')?.textContent).toBe('login page after the fix');
    // An unavailable card does nothing.
    expect(click(document.querySelector(`[data-seq="${refs.seq}"] a.gallery-ref[data-state="unavailable"]`)).defaultPrevented).toBe(false);
  });

  it('preview as [N gallery images] when they are all the message has', () => {
    expect(galleryImagesLabel(1)).toBe('[1 gallery image]');
    expect(galleryImagesLabel(3)).toBe('[3 gallery images]');
    const { sources } = start();
    sources[0]?.emit('entry', { ...refs, text: '', segments: [] });
    const row = document.querySelector('.chat-rows a.chat-row[data-chat="chan:lab/dut1"]');
    expect(row?.querySelector('[data-slot="preview"]')?.textContent).toBe('claude-laptop: [4 gallery images]');
  });
});

describe('attach from gallery', () => {
  it('writes refs in the message and sends no copy', async () => {
    const { http, uploads } = start((_method, path) => (path.startsWith('/api/galleries/') ? page : {}));
    const button = document.querySelector<HTMLButtonElement>('form.composer [data-action="gallery-picker"]');
    expect(button).not.toBeNull();
    button?.click();
    await flush();
    const picker = document.querySelector<HTMLDialogElement>('dialog[data-component="GalleryPicker"]');
    expect(picker?.open).toBe(true);
    // The project has one gallery: it opens on it.
    expect(http.calls.at(-1)?.path).toBe('/api/galleries/lab%2Fshots');
    const picks = Array.from(picker?.querySelectorAll<HTMLButtonElement>('button.gallery-pick') ?? []);
    expect(picks.map((b) => b.dataset['ref'])).toEqual(['[[lab/screens#48433]]', '[[lab/screens#48431]]']);
    expect(picks[1]?.textContent).toBe('Screenshot 48431');
    picks[0]?.click();
    expect(picker?.open).toBe(false);

    // A section: the filter, then the whole section.
    button?.click();
    await flush();
    picker?.querySelector<HTMLButtonElement>('button[data-section="login flow v2"]')?.click();
    await flush();
    expect(http.calls.at(-1)?.query).toEqual({ section: 'login flow v2' });
    picker?.querySelector<HTMLButtonElement>('button[data-ref^="[[lab/shots|"]')?.click();

    const text = document.querySelector<HTMLTextAreaElement>('form.composer textarea');
    expect(text?.value).toBe('[[lab/screens#48433]] [[lab/shots|login flow v2]] ');
    if (text !== null) text.value = `look: ${text.value}`;
    document.querySelector<HTMLFormElement>('form.composer')?.dispatchEvent(new Event('submit', { cancelable: true }));
    await flush();
    expect(uploads.sent).toHaveLength(1);
    expect(uploads.sent[0]?.text).toBe('look: [[lab/screens#48433]] [[lab/shots|login flow v2]] ');
    expect(uploads.sent[0]?.files).toEqual([]);
  });
});
