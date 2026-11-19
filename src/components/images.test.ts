import { afterEach, describe, expect, it } from 'vitest';
import type { Disposable } from '@/core/Disposable.ts';
import type { Attachment, Entry } from '@/model/types.ts';
import { ChatPage } from '@/pages/ChatPage.ts';
import { HomePage } from '@/pages/HomePage.ts';
import { imagesLabel } from '@/render/format.ts';
import { FakeFrames } from '@/testing/FakeFrames.ts';
import { fixture, loadPage } from '@/testing/pages.ts';
import { fakeServices } from '@/testing/services.ts';

const message = fixture('entry_message') as Entry;

function image(sha: string): Attachment {
  return { type: 'image', sha256: sha, mime: 'image/png', width: 1280, height: 800, url: `/blob/${sha}`, thumb_url: `/blob/${sha}?v=thumb`, preview: true };
}

// A screenshot an agent sent with cafard send or image_paths: images, no text.
const shots: Entry = {
  ...message,
  seq: 950,
  chat: 'chan:dut1',
  text: '',
  segments: [],
  sender: { name: 'claude-laptop', kind: 'agent' },
  attachments: [image('a'.repeat(64)), image('b'.repeat(64))],
} as Entry;

let running: Disposable | null = null;
afterEach(() => {
  running?.dispose();
  running = null;
});

describe('screenshots from an agent', () => {
  it('label a message that has only images', () => {
    expect(imagesLabel(0)).toBe('');
    expect(imagesLabel(1)).toBe('[1 image]');
    expect(imagesLabel(2)).toBe('[2 images]');
  });

  it('show as [2 images] in the chat list', () => {
    loadPage('home');
    const { services, sources } = fakeServices(() => message);
    running = new HomePage(document, services).start();
    sources[0]?.emit('entry', shots);
    const row = document.querySelector('.chat-rows a.chat-row[data-chat="chan:dut1"]');
    expect(row?.querySelector('[data-slot="preview"]')?.textContent).toBe('claude-laptop: [2 images]');
    sources[0]?.emit('entry', { ...shots, seq: 951, text: 'login page after fix\nmore' });
    expect(row?.querySelector('[data-slot="preview"]')?.textContent).toBe('claude-laptop: login page after fix');
  });

  it('show thumbnails that open in the viewer', () => {
    loadPage('chat');
    const { services, sources } = fakeServices(() => message);
    const frames = new FakeFrames();
    running = new ChatPage(document, services, frames).start();
    sources[0]?.emit('entry', shots);
    frames.run();
    const row = document.querySelector('[data-component="ConversationView"] [data-seq="950"]');
    const tiles = Array.from(row?.querySelectorAll<HTMLAnchorElement>('a.image') ?? []);
    expect(tiles.map((a) => a.querySelector('img')?.getAttribute('src'))).toEqual([`/blob/${'a'.repeat(64)}?v=thumb`, `/blob/${'b'.repeat(64)}?v=thumb`]);
    const click = new MouseEvent('click', { bubbles: true, cancelable: true });
    tiles[1]?.dispatchEvent(click);
    expect(click.defaultPrevented).toBe(true);
    const viewer = document.querySelector<HTMLDialogElement>('dialog.image-viewer');
    expect(viewer?.open).toBe(true);
    expect(viewer?.querySelector('[data-slot="img"]')?.getAttribute('src')).toBe(`/blob/${'b'.repeat(64)}`);
  });
});
