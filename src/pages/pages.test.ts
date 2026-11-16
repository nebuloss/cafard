import { describe, expect, it } from 'vitest';
import { Backoff } from '@/core/Backoff.ts';
import { MemoryStore } from '@/core/KeyValueStore.ts';
import type { Stream, TokenCreated } from '@/model/types.ts';
import { CafardApi } from '@/net/CafardApi.ts';
import type { HttpClient } from '@/net/HttpClient.ts';
import type { EventSourceLike } from '@/net/LiveFeed.ts';
import { showToken } from '@/pages/AgentsPage.ts';
import { entriesQuery, LivePage } from '@/pages/LivePage.ts';
import { countdown } from '@/pages/PairPage.ts';
import { STREAM_CARD_ATTRIBUTES, StreamCards } from '@/pages/StreamsPage.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { FakeClock } from '@/testing/FakeClock.ts';
import { fixture, loadPage } from '@/testing/pages.ts';
import type { Services } from '@/App.ts';

describe('StreamsPage', () => {
  it('updates a server-rendered card and inserts a new one in order', () => {
    loadPage('streams');
    const rows = document.querySelector<HTMLElement>('[data-slot="rows"]') as HTMLElement;
    const cards = new StreamCards(rows, SlotTemplate.require(document, 'tpl-stream-card', STREAM_CARD_ATTRIBUTES), () => Date.parse('2026-11-16T12:00:00Z'));
    const base = fixture('stream') as Stream;
    const updated = cards.update({ ...base, status: 'down', dropped: 7 });
    expect(updated.dataset['stream']).toBe('serial:dut1');
    expect(updated.dataset['status']).toBe('down');
    expect(updated.querySelector('[data-slot="dropped"]')?.textContent).toBe('7');
    expect(updated.querySelector('[data-slot="line-state"]')?.textContent).toBe('DTR on, RTS off, CTS on, DCD off');
    const fresh = cards.update({ ...base, id: 'serial:dut0', last_line: '<b>hi</b>' });
    expect(rows.querySelector('[data-stream]')).toBe(fresh);
    expect(fresh.querySelector('[data-slot="open"]')?.getAttribute('href')).toBe('/live?streams=serial%3Adut0');
    expect(fresh.querySelector('[data-slot="preview"]')?.textContent).toBe('<b>hi</b>');
    expect(fresh.querySelector('[data-slot="rate"]')?.textContent).toBe('42.6');
  });
});

describe('AgentsPage', () => {
  it('shows a new token once in the server-rendered dialog', () => {
    loadPage('agents');
    const dialog = document.querySelector<HTMLElement>('[data-component="TokenDialog"]') as HTMLElement;
    expect(dialog.hidden).toBe(true);
    showToken(dialog, fixture('token_created') as TokenCreated);
    expect(dialog.hidden).toBe(false);
    expect(dialog.querySelector('[data-slot="token"]')?.textContent).toBe('cfd_7_Qm9vdHN0cmFwLXNlY3JldC1leGFtcGxlLW5vdC1yZWFs');
    expect(dialog.querySelectorAll('.snippet')).toHaveLength(3);
  });
});

describe('PairPage', () => {
  it('counts down to expiry', () => {
    const panel = document.createElement('article');
    panel.dataset['expires'] = '2026-11-16T09:45:00Z';
    const slot = document.createElement('span');
    slot.dataset['slot'] = 'countdown';
    panel.append(slot);
    const clock = new FakeClock(Date.parse('2026-11-16T09:40:00Z'));
    countdown(panel, clock);
    expect(slot.textContent).toBe('Expires in 5:00.');
    clock.advance(61_000);
    expect(slot.textContent).toBe('Expires in 3:59.');
    clock.advance(240_000);
    expect('expired' in panel.dataset).toBe(true);
  });
});

describe('LivePage', () => {
  it('follows from data-next-seq and appends what the feed delivers', () => {
    loadPage('live');
    const sources: { url: string; source: EventSourceLike & { emit(t: string, d: unknown): void } }[] = [];
    const clock = new FakeClock();
    const services: Services = {
      clock,
      storage: new MemoryStore(),
      http: {} as HttpClient,
      api: new CafardApi({} as HttpClient),
      backoff: () => new Backoff({ baseMs: 1000, maxMs: 30000 }),
      eventSources: (url) => {
        const listeners = new Map<string, (e: MessageEvent) => void>();
        const source = {
          onopen: null,
          onerror: null,
          addEventListener: (t: string, l: (e: MessageEvent) => void) => listeners.set(t, l),
          close: () => {},
          emit: (t: string, d: unknown) => listeners.get(t)?.(new MessageEvent(t, { data: JSON.stringify(d) })),
        };
        sources.push({ url, source });
        return source;
      },
    };
    const running = new LivePage(document, services).start();
    expect(sources[0]?.url).toBe('/api/events?since_seq=5');
    sources[0]?.source.emit('entry', { ...(fixture('entry_line') as object), seq: 5, text: 'from the feed' });
    clock.advance(50);
    const last = document.querySelector('.log-list')?.lastElementChild;
    expect(last?.querySelector('[data-slot="text"]')?.textContent).toBe('from the feed');
    running.dispose();
  });

  it('maps the /live query onto /api/entries', () => {
    expect(entriesQuery('streams=serial%3A*&sev=4&from_seq=12&kinds=line')).toEqual({
      streams: 'serial:*',
      channel: undefined,
      sev: '4',
      re: undefined,
      kinds: 'line',
      from_seq: 12,
      to_seq: undefined,
    });
  });
});
