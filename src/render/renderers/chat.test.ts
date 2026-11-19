import { beforeEach, describe, expect, it } from 'vitest';
import type { Chat, Entry, ImageAttachment } from '@/model/types.ts';
import { chatRenderers } from '@/pages/chatParts.ts';
import { logLink, messageTime } from '@/render/format.ts';
import { RendererRegistry } from '@/render/RendererRegistry.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { textSegments } from '@/render/TextSegments.ts';
import { IMAGE_ATTRIBUTES, ImageRenderer } from '@/render/renderers/ImageRenderer.ts';
import { FakeClock } from '@/testing/FakeClock.ts';
import { fixture, loadPage } from '@/testing/pages.ts';

/** The slots of a row, not those of what its containers hold. */
const slots = (el: Element | null): string[] =>
  Array.from(el?.querySelectorAll('[data-slot]') ?? [])
    .filter((s) => s.parentElement?.closest('[data-slot]') === null || !el?.contains(s.parentElement?.closest('[data-slot]') ?? null))
    .map((s) => s.getAttribute('data-slot') ?? '');
const clock = new FakeClock(Date.parse('2026-11-16T12:00:00Z'));
const viewer = { name: 'guillaume', human: true };

describe('chat renderers on the pages the Go template test wrote', () => {
  beforeEach(() => loadPage('chat'));

  it('builds a message like the server does, attachments included', () => {
    const r = chatRenderers(document, viewer, clock);
    const entry = fixture('entry_message') as Entry;
    const row = r.timeline.render(entry);
    expect(row.className).toBe('message');
    expect(row.id).toBe(`m${entry.seq}`);
    expect(row.dataset['senderKind']).toBe('agent');
    expect(row.hasAttribute('data-mine')).toBe(false);
    expect(row.querySelector('[data-slot="sender"]')?.textContent).toBe('claude-laptop');
    expect(row.querySelector('[data-slot="time"]')?.textContent).toBe('09:42');
    const body = row.querySelector('[data-slot="body"]');
    expect(body?.querySelector('code')?.textContent).toBe('dwc3');
    const link = body?.querySelector('a');
    expect(link?.getAttribute('href')).toBe('https://git.example.com/kernel/commit/4a1f');
    expect(link?.getAttribute('target')).toBe('_blank');
    expect(link?.getAttribute('rel')).toBe('noopener noreferrer');
    expect(row.querySelector('a.image')?.getAttribute('href')).toMatch(/^\/blob\/9f2c/);
    expect(row.querySelector('a.image img')?.getAttribute('src')).toMatch(/\?v=thumb$/);
    const card = row.querySelector('.log-card');
    expect(card?.hasAttribute('data-snapshot')).toBe(true);
    expect(card?.querySelector('[data-slot="lines"]')?.textContent).toBe('180 lines');
    expect(card?.querySelectorAll('[data-slot="preview"] .line')).toHaveLength(3);
    expect(card?.querySelector('[data-slot="open"]')?.getAttribute('href')).toBe('/live?from_seq=48211&streams=serial%3Alab%2Fdut1&to_seq=48390');
    expect(row.querySelector('[data-slot="seen-by"]')?.textContent).toBe('seen by guillaume');
    // Same slots as the server's own message row.
    expect(slots(row)).toEqual(slots(document.querySelector('.conversation .message')));
  });

  it('builds a question card whose form answers it', () => {
    const r = chatRenderers(document, viewer, clock);
    const open = { ...(fixture('entry_question') as Entry), question: { choices: ['yes', 'no'], open: true } };
    const card = r.timeline.render(open);
    expect(card.className).toBe('question');
    expect(card.hasAttribute('data-open')).toBe(true);
    const buttons = Array.from(card.querySelectorAll<HTMLButtonElement>('button[name="choice"]'));
    expect(buttons.map((b) => [b.value, b.disabled, b.textContent])).toEqual([['yes', false, 'yes'], ['no', false, 'no']]);
    expect(card.querySelector<HTMLInputElement>('input[name="reply_to"]')?.value).toBe(String(open.seq));
    expect(card.querySelector<HTMLInputElement>('input[name="chat"]')?.value).toBe('chan:lab/dut1');
    expect(card.querySelector('pre code')?.textContent).toBe('flashtool --port /dev/ttyUSB0 write boot.img');
    const answered = r.timeline.render(fixture('entry_question') as Entry);
    expect(answered.hasAttribute('data-open')).toBe(false);
    expect(answered.querySelector('[data-slot="answer"]')?.textContent).toBe('answered by guillaume: yes');
    expect(Array.from(answered.querySelectorAll<HTMLButtonElement>('button[name="choice"]')).every((b) => b.disabled)).toBe(true);
    expect(slots(card)).toEqual(slots(document.querySelector('.conversation .question')));
  });

  it('builds event rows and lines', () => {
    const r = chatRenderers(document, viewer, clock);
    const marker = { ...(fixture('entry_marker') as Entry), event: true };
    const row = r.timeline.render(marker);
    expect(row.className).toBe('event-row');
    expect(row.dataset['kind']).toBe('marker');
    expect(row.querySelector('[data-slot="open"]')?.getAttribute('href')).toBe(`/live?streams=serial%3Alab%2Fdut1&to_seq=${marker.seq}`);
    expect(slots(row)).toEqual(slots(document.querySelector('.conversation .event-row')));
    expect(r.logs?.render(fixture('entry_line') as Entry).className).toBe('line');
    expect(r.logs?.render(fixture('entry_marker') as Entry).className).toBe('marker');
  });

  it('builds chat rows like the list', () => {
    const r = chatRenderers(document, viewer, clock);
    const row = r.rows?.render(fixture('chat_channel') as Chat);
    expect(row?.getAttribute('href')).toBe('/c/lab/dut1');
    expect(row?.hasAttribute('data-pinned')).toBe(true);
    expect(row?.querySelector('[data-slot="name"]')?.textContent).toBe('#dut1');
    expect(row?.querySelector('[data-slot="unread"]')?.textContent).toBe('3');
    expect(row?.querySelector('[data-slot="preview"]')?.textContent).toBe('claude-laptop: Should I flash the board with the patched kernel?');
    expect(row?.querySelectorAll('.stream-dot')).toHaveLength(2);
    expect(slots(row ?? null)).toEqual(slots(document.querySelector('a.chat-row')));
    const direct = r.rows?.render(fixture('chat_direct') as Chat);
    expect(direct?.getAttribute('href')).toBe('/dm/codex-build');
  });

  it('keeps hostile message text as text', () => {
    const r = chatRenderers(document, viewer, clock);
    const evil = { ...(fixture('entry_message') as Entry), segments: [{ t: 'text', v: '<img src=x onerror=alert(1)>' }, { t: 'link', v: 'x', href: 'javascript:alert(2)' }] } as Entry;
    const body = r.timeline.render(evil).querySelector('[data-slot="body"]');
    expect(body?.querySelector('img, a')).toBeNull();
    expect(body?.textContent).toBe('<img src=x onerror=alert(1)>x');
  });

  it('falls back to an event row for what no rule takes', () => {
    const r = chatRenderers(document, viewer, clock);
    expect((r.timeline as RendererRegistry).render({ ...(fixture('entry_line') as Entry), event: true }).className).toBe('event-row');
  });
});

describe('home renderers', () => {
  it('builds a needs-you card with its chat link', () => {
    loadPage('home');
    const r = chatRenderers(document, viewer, clock, { back: '/', next: '/' });
    const card = r.needsYou?.render({ ...(fixture('entry_question') as Entry), question: { choices: ['yes'], open: true } });
    expect(card?.querySelector('[data-slot="chat"]')?.getAttribute('href')).toBe('/c/lab/dut1#m48410');
    expect(card?.querySelector('[data-slot="chat"]')?.textContent).toBe('in #lab/dut1');
    expect(card?.querySelector<HTMLInputElement>('input[name="next"]')?.value).toBe('/');
    expect(slots(card ?? null)).toEqual(slots(document.querySelector('.needs-you .question')));
  });
});

describe('image grid', () => {
  it('shows four tiles and +N', () => {
    loadPage('chat');
    const images = new ImageRenderer(SlotTemplate.find(document, 'tpl-image', IMAGE_ATTRIBUTES)!);
    const img = (fixture('attachment_image') as ImageAttachment);
    const grid = images.grid([img, img, img, img, img, img]);
    expect(grid?.dataset['count']).toBe('4');
    expect(grid?.querySelectorAll('a.image')).toHaveLength(4);
    expect(grid?.lastElementChild?.querySelector('[data-slot="more"]')?.textContent).toBe('+2');
    const heic = images.render({ ...img, preview: false });
    expect(heic.querySelector('img')).toBeNull();
    expect(heic.querySelector('[data-slot="img"]')?.textContent).toContain('HEIC');
    expect(images.grid([])).toBeNull();
  });
});

describe('formatting shared with Go', () => {
  it('matches pages.MessageTime and pages.LogLink', () => {
    const now = Date.parse('2026-11-16T12:00:00Z');
    expect(messageTime('2026-11-16T09:42:30.5Z', now)).toBe('09:42');
    expect(messageTime('2026-11-15T09:42:30Z', now)).toBe('Nov 15 09:42');
    expect(logLink({ streams: ['serial:lab/dut1', 'syslog:x'], from_seq: 3, to_seq: null, grep: 'usb', min_severity: 3 })).toBe('/live?from_seq=3&re=usb&sev=3&streams=serial%3Alab%2Fdut1%2Csyslog%3Ax');
  });

  it('renders segments without parsing HTML', () => {
    const nodes = textSegments([{ t: 'text', v: 'a\n' }, { t: 'codeblock', v: 'x', lang: 'sh' }]);
    expect(nodes[0]).toBe('a\n');
    expect((nodes[1] as HTMLElement).tagName).toBe('PRE');
    expect(textSegments(undefined, 'plain')).toEqual(['plain']);
  });
});
