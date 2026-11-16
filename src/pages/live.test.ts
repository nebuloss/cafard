import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Disposable } from '@/core/Disposable.ts';
import type { Entry } from '@/model/types.ts';
import { LONG_PRESS_MS } from '@/components/LogsPane.ts';
import { LivePage } from '@/pages/LivePage.ts';
import { FakeFrames } from '@/testing/FakeFrames.ts';
import { fixture, loadPage } from '@/testing/pages.ts';
import { fakeServices, flush } from '@/testing/services.ts';

const line = fixture('entry_line') as Entry;
const message = fixture('entry_message') as Entry;

let running: Disposable | null = null;
afterEach(() => {
  running?.dispose();
  running = null;
  vi.useRealTimers();
});

function start(): { frames: FakeFrames; feed: ReturnType<typeof fakeServices>['sources'][number] | undefined; uploads: ReturnType<typeof fakeServices>['uploads'] } {
  loadPage('live');
  const { services, sources, uploads } = fakeServices((job) => ({ ...message, seq: 900, chat: job.chat, text: job.text }));
  const frames = new FakeFrames();
  running = new LivePage(document, services, frames).start();
  return { frames, feed: sources[0], uploads };
}

const rows = (): HTMLElement[] => Array.from(document.querySelectorAll<HTMLElement>('.log-list > [data-seq]'));

describe('LivePage', () => {
  it('follows from data-next-seq and paints what the feed delivers on the next frame', () => {
    const { frames, feed } = start();
    const nextSeq = Number(document.querySelector('[data-component="LogView"]')?.getAttribute('data-next-seq'));
    expect(feed?.url).toBe(`/api/events?since_seq=${nextSeq}`);
    const before = rows().length;
    feed?.emit('entry', { ...line, seq: nextSeq, text: 'from the feed' });
    feed?.emit('entry', { ...line, seq: nextSeq + 1, text: 'and another' });
    expect(rows()).toHaveLength(before);
    expect(frames.run()).toBe(1);
    expect(rows()).toHaveLength(before + 2);
    expect(rows().at(-1)?.querySelector('[data-slot="text"]')?.textContent).toBe('and another');
  });

  it('shows a message of kinds=message as a line with its chat and sender', () => {
    const { frames, feed } = start();
    feed?.emit('entry', { ...message, seq: 9000, chat: 'dm:codex-build', text: 'build 812 is green' });
    frames.run();
    const row = rows().at(-1);
    expect(row?.dataset['kind']).toBe('message');
    expect(row?.querySelector('[data-slot="stream"]')?.textContent).toBe('@codex-build');
    expect(row?.querySelector('[data-slot="text"]')?.textContent).toBe('claude-laptop: build 812 is green');
    // A message row offers the copy actions only.
    row?.click();
    const menu = document.querySelector<HTMLElement>('[data-slot="line-menu"]');
    expect(menu?.hidden).toBe(false);
    expect(menu?.querySelector<HTMLElement>('[data-action="attach-line"]')?.hidden).toBe(true);
    expect(menu?.querySelector<HTMLElement>('[data-action="copy-line"]')?.hidden).toBe(false);
  });

  it('attaches a line through the quick drop sheet, which sends it as a log attachment', async () => {
    const { uploads } = start();
    const first = rows().find((r) => r.classList.contains('line'));
    expect(first).toBeDefined();
    first?.click();
    document.querySelector<HTMLElement>('[data-action="attach-line"]')?.click();
    const sheet = document.querySelector<HTMLDialogElement>('[data-component="QuickDropSheet"]');
    expect(sheet?.open).toBe(true);
    const chip = sheet?.querySelector('[data-slot="tray"] [data-kind="log"]');
    expect(chip?.textContent).toContain('serial:dut1');
    sheet?.querySelector<HTMLFormElement>('form.drop-form')?.requestSubmit();
    await flush();
    const seq = Number(first?.dataset['seq']);
    expect(uploads.sent[0]?.chat).toBe('chan:dut1');
    expect(uploads.sent[0]?.logs).toEqual([{ stream: 'serial:dut1', from_seq: seq, to_seq: seq }]);
  });

  it('selects a range with click then shift-click, and with a long press on a phone', () => {
    start();
    const lines = rows().filter((r) => r.classList.contains('line'));
    const [a, b] = [lines[0], lines[1]];
    a?.dispatchEvent(new MouseEvent('click', { bubbles: true, shiftKey: true }));
    b?.dispatchEvent(new MouseEvent('click', { bubbles: true, shiftKey: true }));
    const bar = document.querySelector<HTMLElement>('[data-component="RangeBar"]');
    expect(bar?.hidden).toBe(false);
    expect(bar?.querySelector('[data-slot="count"]')?.textContent).toBe('2 lines');
    bar?.querySelector<HTMLElement>('[data-action="cancel"]')?.click();
    expect(bar?.hidden).toBe(true);

    vi.useFakeTimers();
    a?.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerType: 'touch', clientX: 5, clientY: 5 }));
    vi.advanceTimersByTime(LONG_PRESS_MS + 10);
    a?.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerType: 'touch' }));
    a?.click(); // the click that ends the long press is not a tap
    expect(bar?.hidden).toBe(false);
    expect(bar?.querySelector('[data-slot="count"]')?.textContent).toBe('Tap the last line');
    b?.click();
    expect(bar?.querySelector('[data-slot="count"]')?.textContent).toBe('2 lines');
  });
});
