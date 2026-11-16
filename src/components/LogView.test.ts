import { beforeEach, describe, expect, it } from 'vitest';
import { disposable, type Disposable } from '@/core/Disposable.ts';
import { LogView, type Scroller } from '@/components/LogView.ts';
import type { Entry } from '@/model/types.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';
import { LINE_ATTRIBUTES, LineRenderer } from '@/render/renderers/LineRenderer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { FakeFrames } from '@/testing/FakeFrames.ts';
import { fixture, loadPage } from '@/testing/pages.ts';

class EndScroller implements Scroller {
  end = true;
  private listener: (() => void) | null = null;
  atEnd(): boolean {
    return this.end;
  }
  toEnd(): void {
    this.end = true;
  }
  onScroll(listener: () => void): Disposable {
    this.listener = listener;
    return disposable(() => (this.listener = null));
  }
  userScrolls(end: boolean): void {
    this.end = end;
    this.listener?.();
  }
}

/** Counts what reaches the renderer: the cost of a frame. */
class Counting implements EntryRenderer {
  rendered = 0;
  constructor(private readonly inner: EntryRenderer) {}
  render(entry: Entry): HTMLElement {
    this.rendered++;
    return this.inner.render(entry);
  }
}

const base = fixture('entry_line') as Entry;
const line = (seq: number): Entry => ({ ...base, seq, text: `line ${seq}` });

describe('LogView under a fast feed (1000 lines/s)', () => {
  let root: HTMLElement;
  let list: HTMLElement;
  let frames: FakeFrames;
  let renderer: Counting;
  let scroller: EndScroller;
  let view: LogView;

  beforeEach(() => {
    loadPage('live');
    root = document.querySelector<HTMLElement>('[data-component="LogView"]') as HTMLElement;
    list = root.querySelector<HTMLElement>('[data-slot="list"]') as HTMLElement;
    list.replaceChildren();
    frames = new FakeFrames();
    renderer = new Counting(new LineRenderer(SlotTemplate.require(document, 'tpl-line', LINE_ATTRIBUTES)));
    scroller = new EndScroller();
    view = new LogView(root, renderer, scroller, document, LogView.cap, frames);
    view.mount();
  });

  it('paints 10000 entries in one batch per frame and keeps 5000 nodes', () => {
    // 1000 lines per second at 60 frames per second: about 17 per frame.
    let painted = 0;
    for (let seq = 1; seq <= 10000; seq++) {
      view.enqueue([line(seq)]);
      expect(list.children.length).toBe(Math.min(painted, LogView.cap)); // nothing before the frame
      if (seq % 17 === 0) {
        expect(frames.pending).toBe(1); // one frame asked for, however many entries came
        frames.run();
        painted = seq;
      }
    }
    frames.run();
    expect(frames.requested).toBe(Math.ceil(10000 / 17));
    expect(view.flushes).toBe(frames.requested);
    expect(list.children.length).toBe(LogView.cap);
    expect(list.firstElementChild?.getAttribute('data-seq')).toBe('5001');
    expect(list.lastElementChild?.getAttribute('data-seq')).toBe('10000');
    expect(view.cursor).toBe(10000);
  });

  it('renders only the newest cap of a burst that arrives within one frame', () => {
    for (let seq = 1; seq <= 10000; seq++) view.enqueue([line(seq)]);
    expect(frames.requested).toBe(1);
    frames.run();
    expect(view.flushes).toBe(1);
    expect(renderer.rendered).toBe(LogView.cap);
    expect(list.children.length).toBe(LogView.cap);
    expect(list.firstElementChild?.getAttribute('data-seq')).toBe('5001');
  });

  it('keeps new entries out of the DOM while paused, then shows them on resume', () => {
    for (let seq = 1; seq <= 100; seq++) view.enqueue([line(seq)]);
    frames.run();
    scroller.userScrolls(false);
    expect(view.follow.value.mode).toBe('paused');
    for (let seq = 101; seq <= 10100; seq++) view.enqueue([line(seq)]);
    frames.run();
    expect(list.children.length).toBe(100);
    expect(list.lastElementChild?.getAttribute('data-seq')).toBe('100');
    expect(view.follow.value).toEqual({ mode: 'paused', newCount: 10000 });
    expect(root.querySelector('[data-slot="new-count"]')?.textContent).toBe('10000 new');
    expect(view.waiting).toBe(LogView.cap);
    view.resume();
    expect(list.children.length).toBe(LogView.cap);
    expect(list.lastElementChild?.getAttribute('data-seq')).toBe('10100');
    expect(view.waiting).toBe(0);
  });

  it('cancels a pending frame when unmounted', () => {
    const mounted = new LogView(root, renderer, scroller, document, 10, frames);
    const handle = mounted.mount();
    mounted.enqueue([line(1)]);
    const before = frames.pending;
    handle.dispose();
    expect(frames.pending).toBe(before - 1);
  });
});
