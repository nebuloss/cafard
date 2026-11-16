import { beforeEach, describe, expect, it, vi } from 'vitest';
import { disposable, type Disposable } from '@/core/Disposable.ts';
import { Component } from '@/components/Component.ts';
import { LogView, type Scroller } from '@/components/LogView.ts';
import { Toast } from '@/components/Toast.ts';
import type { Entry } from '@/model/types.ts';
import { LINE_ATTRIBUTES, LineRenderer } from '@/render/renderers/LineRenderer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { FakeClock } from '@/testing/FakeClock.ts';
import { fixture, loadPage } from '@/testing/pages.ts';

class FakeScroller implements Scroller {
  end = true;
  toEndCalls = 0;
  private listener: (() => void) | null = null;
  atEnd(): boolean {
    return this.end;
  }
  toEnd(): void {
    this.toEndCalls++;
    this.end = true;
    this.listener?.();
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

const line = (seq: number): Entry => ({ ...(fixture('entry_line') as Entry), seq });

describe('Component', () => {
  it('runs build then bind once, and unmount releases the bag', () => {
    const calls: string[] = [];
    const released = vi.fn();
    class Probe extends Component {
      protected override build(): void {
        calls.push('build');
      }
      protected bind(): void {
        calls.push('bind');
        this.bag.defer(released);
      }
    }
    const probe = new Probe(document.createElement('div'));
    const mounted = probe.mount();
    expect(calls).toEqual(['build', 'bind']);
    expect(() => probe.mount()).toThrow();
    mounted.dispose();
    expect(released).toHaveBeenCalledTimes(1);
  });
});

describe('LogView', () => {
  let root: HTMLElement;
  let scroller: FakeScroller;
  let view: LogView;
  beforeEach(() => {
    loadPage('live');
    root = document.querySelector<HTMLElement>('[data-component="LogView"]') as HTMLElement;
    scroller = new FakeScroller();
    view = new LogView(root, new LineRenderer(SlotTemplate.require(document, 'tpl-line', LINE_ATTRIBUTES)), scroller, document, 10);
    view.mount();
  });

  it('continues after the server-rendered rows and skips what it already shows', () => {
    const shown = view.size;
    expect(view.cursor).toBe(4);
    view.append([line(3), line(4), line(5)]);
    expect(view.size).toBe(shown + 1);
    expect(view.cursor).toBe(5);
  });

  it('keeps at most cap rows, dropping the oldest', () => {
    view.append(Array.from({ length: 30 }, (_, i) => line(10 + i)));
    expect(view.size).toBe(10);
    expect(root.querySelector('[data-slot="list"]')?.firstElementChild?.getAttribute('data-seq')).toBe('30');
  });

  it('pauses when the reader scrolls up, counts new lines, and resumes', () => {
    const button = root.querySelector<HTMLButtonElement>('[data-slot="new"]') as HTMLButtonElement;
    scroller.userScrolls(false);
    expect(view.follow.value).toEqual({ mode: 'paused', newCount: 0 });
    view.append([line(10), line(11)]);
    expect(view.follow.value).toEqual({ mode: 'paused', newCount: 2 });
    expect(button.hidden).toBe(false);
    expect(root.querySelector('[data-slot="new-count"]')?.textContent).toBe('2 new');
    button.click();
    expect(view.follow.value.mode).toBe('following');
    expect(button.hidden).toBe(true);
    scroller.end = false;
    view.append([line(12)]);
    expect(scroller.toEndCalls).toBeGreaterThan(0);
  });

  it('toggles with f, but not while typing', () => {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'f' }));
    expect(view.follow.value.mode).toBe('paused');
    const input = document.querySelector<HTMLInputElement>('input[name="re"]') as HTMLInputElement;
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'f', bubbles: true }));
    expect(view.follow.value.mode).toBe('paused');
    root.querySelector<HTMLButtonElement>('[data-slot="follow"]')?.click();
    expect(view.follow.value.mode).toBe('following');
  });
});

describe('Toast', () => {
  it('shows text from tpl-toast and removes it after a while', () => {
    loadPage('live');
    const clock = new FakeClock();
    const region = document.querySelector<HTMLElement>('[data-component="Toast"]') as HTMLElement;
    const toast = new Toast(region, clock, document);
    toast.mount();
    const run = vi.fn();
    const el = toast.show('<b>not bold</b>', 'error', { label: 'Retry', run });
    expect(el?.querySelector('[data-slot="text"]')?.textContent).toBe('<b>not bold</b>');
    expect(el?.dataset['tone']).toBe('error');
    el?.querySelector('button')?.click();
    expect(run).toHaveBeenCalled();
    expect(region.children).toHaveLength(0);
    for (let i = 0; i < 5; i++) toast.show(`n${i}`);
    expect(region.children).toHaveLength(3);
    clock.advance(Toast.showFor);
    expect(region.children).toHaveLength(0);
  });
});
