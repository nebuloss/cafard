import { describe, expect, it } from 'vitest';
import type { Stream, TokenCreated } from '@/model/types.ts';
import { showToken } from '@/pages/AgentsPage.ts';
import { entriesQuery } from '@/net/CafardApi.ts';
import { countdown } from '@/pages/PairPage.ts';
import { STREAM_CARD_ATTRIBUTES, StreamCards } from '@/pages/StreamsPage.ts';
import { SlotTemplate } from '@/render/SlotTemplate.ts';
import { FakeClock } from '@/testing/FakeClock.ts';
import { fixture, loadPage } from '@/testing/pages.ts';

describe('StreamsPage', () => {
  it('updates a server-rendered card and inserts a new one in order', () => {
    loadPage('streams');
    const rows = document.querySelector<HTMLElement>('[data-slot="rows"]') as HTMLElement;
    const cards = new StreamCards(rows, SlotTemplate.find(document, 'tpl-stream-card', STREAM_CARD_ATTRIBUTES)!, () => Date.parse('2026-11-16T12:00:00Z'));
    const base = fixture('stream') as Stream;
    const updated = cards.update({ ...base, status: 'down', dropped: 7 });
    expect(updated.dataset['stream']).toBe('serial:lab/dut1');
    expect(updated.dataset['status']).toBe('down');
    expect(updated.querySelector('[data-slot="dropped"]')?.textContent).toBe('7');
    expect(updated.querySelector('[data-slot="line-state"]')?.textContent).toBe('DTR on, RTS off, CTS on, DCD off');
    expect(updated.querySelector('[data-slot="claim"] .chip')?.textContent).toBe('claude-laptop until 10:12');
    expect(updated.querySelector('[data-slot="preview"]')?.textContent).toBe('[   76.445871] usb usb1-port1: connect-debounce failed');
    const { claim: _claim, ...released } = base;
    cards.update(released);
    expect(updated.querySelector('[data-slot="claim"]')?.children).toHaveLength(0);
    const fresh = cards.update({ ...base, id: 'serial:dut0', last_line: '<b>hi</b>' });
    expect(rows.querySelector('[data-stream]')).toBe(fresh);
    expect(fresh.querySelector('[data-slot="open"]')?.getAttribute('href')).toBe('/live?streams=serial%3Adut0');
    expect(fresh.querySelector('[data-slot="preview"]')?.textContent).toBe('<b>hi</b>');
    expect(fresh.querySelector('[data-slot="rate"]')?.textContent).toBe('42.6');
    const other = cards.update({ ...base, id: 'syslog:zz/box', project: 'zz' });
    const group = other.closest<HTMLElement>('.stream-group');
    expect(group?.dataset['project']).toBe('zz');
    expect(group?.querySelector('[role="rowheader"]')?.textContent).toBe('zz');
    expect(fresh.closest<HTMLElement>('.stream-group')?.dataset['project']).toBe('lab');
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
