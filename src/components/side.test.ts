import { afterEach, describe, expect, it } from 'vitest';
import type { Disposable } from '@/core/Disposable.ts';
import type { Chat, Entry, Stream } from '@/model/types.ts';
import { sectionOf } from '@/components/ChatList.ts';
import { cardPreview, projectTotals } from '@/components/ProjectCards.ts';
import { sinkStatus } from '@/components/SideNav.ts';
import { ChatPage } from '@/pages/ChatPage.ts';
import { GalleryPage } from '@/pages/GalleryPage.ts';
import { LivePage } from '@/pages/LivePage.ts';
import { PairPage } from '@/pages/PairPage.ts';
import { badgeCount, chatHref, logsHref, projectHref } from '@/render/format.ts';
import { FakeFrames } from '@/testing/FakeFrames.ts';
import { fixture, loadPage } from '@/testing/pages.ts';
import { fakeServices, flush } from '@/testing/services.ts';

const message = fixture('entry_message') as Entry;
const stream = fixture('stream') as Stream;

let running: Disposable | null = null;
afterEach(() => {
  running?.dispose();
  running = null;
});

const side = (): HTMLElement => document.querySelector<HTMLElement>('[data-component="SideNav"]') as HTMLElement;
const key = (k: string): void => void document.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));

describe('the project view addresses', () => {
  it('match the Go builders', () => {
    expect(projectHref('lab')).toBe('/projects/lab');
    expect(chatHref('chan:lab/dut1')).toBe('/projects/lab/c/dut1');
    expect(chatHref('dm:codex-build')).toBe('/dm/codex-build');
    expect(logsHref('lab')).toBe('/projects/lab/logs');
    expect(logsHref('lab', 'bench-pi')).toBe('/projects/lab/logs/bench-pi');
    expect(badgeCount(0)).toBe('');
    expect(badgeCount(120)).toBe('99+');
  });

  it('sum a sink up and a project up like the server', () => {
    expect(sinkStatus(['idle', 'connected'])).toBe('connected');
    expect(sinkStatus(['connected', 'down'])).toBe('down');
    expect(sinkStatus(['idle'])).toBe('idle');
    expect(sinkStatus([])).toBe('');
    const totals = projectTotals({ open_questions: 1, unread_direct: 2, sources_down: 0, chats: [
      { id: 'chan:lab/dut1', unread: 3, open_questions: 1 }, { id: 'chan:lab/general', unread: 2, open_questions: 0 },
      { id: 'chan:rack/x', unread: 1, open_questions: 0 }, { id: 'dm:codex', unread: 2, open_questions: 0 },
    ] });
    expect(totals.get('lab')).toEqual({ unread: 5, questions: 1 });
    expect(totals.get('rack')).toEqual({ unread: 1, questions: 0 });
    expect(totals.has('codex')).toBe(false);
    expect(cardPreview({ ...message, chat: 'chan:lab/dut1', text: 'image v42 ready\nmore' })).toBe('#dut1 claude-laptop: image v42 ready');
    expect(sectionOf({ ...(fixture('chat_gallery') as Chat) })).toBe('galleries');
  });
});

describe('the side list', () => {
  it('opens as a drawer from the top bar and closes on Esc, the scrim or its close link', () => {
    loadPage('chat');
    const { services } = fakeServices(() => message);
    running = new ChatPage(document, services, new FakeFrames()).start();
    const open = document.querySelector<HTMLAnchorElement>('[data-action="open-side"]');
    // Without the bundle the same link opens it through #side.
    expect(open?.getAttribute('href')).toBe('#side');
    const click = new MouseEvent('click', { bubbles: true, cancelable: true });
    open?.dispatchEvent(click);
    expect(click.defaultPrevented).toBe(true);
    expect(side().hasAttribute('data-open')).toBe(true);
    expect(open?.getAttribute('aria-expanded')).toBe('true');
    expect(document.activeElement?.getAttribute('href')).toBe('/projects/lab/c/dut1');
    key('Escape');
    expect(side().hasAttribute('data-open')).toBe(false);
    for (const selector of ['.side-scrim', '.side-close']) {
      open?.click();
      expect(side().hasAttribute('data-open')).toBe(true);
      document.querySelector<HTMLElement>(selector)?.click();
      expect(side().hasAttribute('data-open')).toBe(false);
    }
  });

  it('keeps its badges, the switcher and the sinks live', () => {
    loadPage('chat');
    const { services, sources } = fakeServices(() => message);
    running = new ChatPage(document, services, new FakeFrames()).start();
    const feed = sources[0];
    feed?.emit('counts', { open_questions: 0, unread_direct: 4, sources_down: 0, chats: [{ id: 'chan:lab/general', unread: 7, open_questions: 0 }] });
    const general = side().querySelector('a.chat-row[data-chat="chan:lab/general"]');
    expect(general?.querySelector('[data-slot="unread"]')?.textContent).toBe('7');
    expect(side().querySelector('a.chat-row[data-chat="chan:lab/dut1"] [data-slot="unread"]')?.textContent).toBe('');
    expect(side().querySelector('[data-switch="lab"] [data-slot="unread"]')?.textContent).toBe('7');
    expect(side().querySelector('[data-slot="direct"]')?.textContent).toBe('4');

    const sink = side().querySelector<HTMLElement>('[data-sink="lab/dut1"]');
    expect(sink?.dataset['status']).toBe('connected');
    feed?.emit('stream_status', { ...stream, id: 'serial:lab/dut1', project: 'lab', sink: 'dut1', status: 'down', last_activity: '2026-11-16T11:58:00Z' });
    expect(sink?.dataset['status']).toBe('down');
    expect(sink?.querySelector('.stream-dot')?.getAttribute('title')).toBe('serial:lab/dut1: down');
    expect(sink?.querySelector('[data-slot="time"]')?.textContent).toBe('11:58');
    // A transport the sink had not used yet gets a dot of its own.
    feed?.emit('stream_status', { ...stream, id: 'push:lab/dut1', project: 'lab', sink: 'dut1', status: 'connected' });
    expect(sink?.querySelectorAll('.stream-dot')).toHaveLength(2);
    expect(sink?.dataset['status']).toBe('down');
    // Another project's stream changes nothing here.
    feed?.emit('stream_status', { ...stream, id: 'serial:rack/dut1', project: 'rack', sink: 'dut1', status: 'connected' });
    expect(sink?.querySelectorAll('.stream-dot')).toHaveLength(2);
  });

  it('puts a new gallery under Galleries and a new chat under Chats', () => {
    loadPage('gallery');
    const { services, sources } = fakeServices(() => message);
    running = new GalleryPage(document, services).start();
    const gallery = { ...(fixture('chat_gallery') as Chat), id: 'chan:lab/screens', name: 'screens', project: 'lab' };
    sources[0]?.emit('chat', gallery);
    expect(side().querySelector('[data-section="galleries"] a.chat-row[data-chat="chan:lab/screens"]')?.getAttribute('href')).toBe('/projects/lab/g/screens');
    sources[0]?.emit('chat', { ...(fixture('chat_channel') as Chat), id: 'chan:lab/run-42', name: 'run-42', project: 'lab', archived: false, followed: true });
    expect(side().querySelector('[data-section="channels"] a.chat-row[data-chat="chan:lab/run-42"]')?.getAttribute('href')).toBe('/projects/lab/c/run-42');
  });

  it('makes a channel in its project from the New channel dialog', async () => {
    loadPage('chat');
    const { services, http } = fakeServices(() => message, () => ({ ...(fixture('chat_channel') as Chat), id: 'chan:lab/run-42', name: 'run-42' }));
    const assigned: string[] = [];
    const real = window.location;
    Object.defineProperty(window, 'location', { configurable: true, value: { ...real, assign: (p: string) => assigned.push(p), hash: '', search: '', pathname: '/projects/lab/c/dut1' } });
    running = new ChatPage(document, services, new FakeFrames()).start();
    const dialog = document.querySelector<HTMLDialogElement>('[data-component="NewChannel"]');
    expect(dialog?.querySelector<HTMLInputElement>('input[name="project"]')?.value).toBe('lab');
    document.querySelector<HTMLElement>('[data-action="new-channel"]')?.click();
    const name = dialog?.querySelector<HTMLInputElement>('input[name="name"]');
    if (name) name.value = 'run-42';
    dialog?.querySelector<HTMLElement>('[data-action="create"]')?.click();
    await flush();
    expect(http.calls.find((c) => c.path === '/api/channels')?.body).toEqual({ name: 'run-42', project: 'lab' });
    expect(assigned).toEqual(['/projects/lab/c/run-42']);
    Object.defineProperty(window, 'location', { configurable: true, value: real });
  });
});

describe("a project's logs", () => {
  it('follow the sources of their sink only', () => {
    loadPage('logs');
    const { services, sources } = fakeServices(() => message);
    running = new LivePage(document, services, new FakeFrames()).start();
    expect(sources[0]?.url).toContain('streams=%2A%3Alab%2Fdut1');
    const rows = document.querySelector('[data-component="StreamList"] [data-slot="rows"]');
    const cards = (): string[] => Array.from(rows?.querySelectorAll<HTMLElement>('[data-stream]') ?? []).map((c) => c.dataset['stream'] ?? '');
    expect(cards()).toEqual(['serial:lab/dut1']);
    sources[0]?.emit('stream_status', { ...stream, id: 'serial:lab/dut1', project: 'lab', sink: 'dut1', status: 'down' });
    expect(rows?.querySelector<HTMLElement>('[data-stream="serial:lab/dut1"]')?.dataset['status']).toBe('down');
    sources[0]?.emit('stream_status', { ...stream, id: 'syslog:lab/bench-pi', project: 'lab', sink: 'bench-pi' });
    sources[0]?.emit('stream_status', { ...stream, id: 'serial:rack/dut1', project: 'rack', sink: 'dut1' });
    sources[0]?.emit('stream_status', { ...stream, id: 'push:lab/dut1', project: 'lab', sink: 'dut1' });
    expect(cards()).toEqual(['push:lab/dut1', 'serial:lab/dut1']);
    // The filter form stays on the page, which picks the streams itself.
    expect(document.querySelector('form.filter-bar')?.getAttribute('action')).toBe('/projects/lab/logs/dut1');
    expect(document.querySelector('form.filter-bar input[name="streams"]')).toBeNull();
  });

  it('keep the side list on the plain pages of the view', () => {
    loadPage('direct');
    const { services, sources } = fakeServices(() => message);
    running = new PairPage(document, services).start();
    sources[0]?.emit('counts', { open_questions: 0, unread_direct: 2, sources_down: 0, chats: [{ id: 'dm:codex-build', unread: 2, open_questions: 0 }] });
    expect(side().querySelector('a.chat-row[data-chat="dm:codex-build"] [data-slot="unread"]')?.textContent).toBe('2');
  });
});
