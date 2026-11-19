import { beforeEach, describe, expect, it } from 'vitest';
import type { Entry } from '@/model/types.ts';
import { RendererRegistry } from '@/render/RendererRegistry.ts';
import { LINE_ATTRIBUTES, LineRenderer } from '@/render/renderers/LineRenderer.ts';
import { MARKER_ATTRIBUTES, MarkerRenderer } from '@/render/renderers/MarkerRenderer.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { fixture, loadPage } from '@/testing/pages.ts';

describe('renderers on the page the Go template test wrote', () => {
  let lines: LineRenderer;
  let markers: MarkerRenderer;
  beforeEach(() => {
    loadPage('live');
    lines = new LineRenderer(SlotTemplate.find(document, 'tpl-line', LINE_ATTRIBUTES)!);
    markers = new MarkerRenderer(SlotTemplate.find(document, 'tpl-marker', MARKER_ATTRIBUTES)!);
  });

  it('builds a line the way the server renders one', () => {
    const entry = fixture('entry_line') as Entry;
    const row = lines.render(entry);
    expect(row.className).toBe('line');
    expect(row.dataset['seq']).toBe('48213');
    expect(row.dataset['sev']).toBe('3');
    expect(row.dataset['tone']).toBe('1');
    expect(row.querySelector('[data-slot="time"]')?.textContent).toBe('09:41:02.512');
    expect(row.querySelector('[data-slot="sev"]')?.textContent).toBe('E');
    expect(row.querySelector('[data-slot="sev"]')?.getAttribute('title')).toBe('ERR');
    expect(row.querySelector('[data-slot="text"]')?.textContent).toBe(entry.text);
    // The server's own row for a line has the same structure.
    const server = document.querySelector('.log-list .line');
    const slots = (el: Element | null): string[] => Array.from(el?.querySelectorAll('[data-slot]') ?? []).map((s) => s.getAttribute('data-slot') ?? '');
    expect(slots(row)).toEqual(slots(server));
  });

  it('keeps hostile text as text and ANSI as classes', () => {
    const entry = { ...(fixture('entry_line') as Entry), text: '\x1b[31m<script>alert(1)</script>\x1b[0m <img src=x onerror=alert(2)>' };
    const text = lines.render(entry).querySelector('[data-slot="text"]');
    expect(text?.querySelector('script, img')).toBeNull();
    expect(text?.textContent).toBe('<script>alert(1)</script> <img src=x onerror=alert(2)>');
    expect(text?.querySelector('span')?.className).toBe('ansi-fg-1');
  });

  it('builds a marker row', () => {
    const row = markers.render(fixture('entry_marker') as Entry);
    expect(row.className).toBe('marker');
    expect(row.querySelector('[data-slot="text"]')?.textContent).toBe('serial:lab/dut1: flashed linux-6.18-rc3 to dut1');
  });

  it('refuses attributes outside the contract list', () => {
    const t = SlotTemplate.find(document, 'tpl-line', LINE_ATTRIBUTES)!.clone();
    expect(() => t.attr({ href: '/x' })).toThrow();
    expect(() => t.attr({ 'data-selected': true })).not.toThrow();
  });

  it('falls back for kinds nobody registered', () => {
    const registry = new RendererRegistry(lines).register('marker', markers);
    const message = { ...(fixture('entry_message') as Entry) };
    expect(registry.render(message).className).toBe('line');
    expect(registry.render(fixture('entry_marker') as Entry).className).toBe('marker');
    const special = registry.when((e) => e.meta?.['client'] === 'claude-code', markers);
    expect(special.render(message).className).toBe('marker');
  });
});
