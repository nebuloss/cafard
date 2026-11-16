import { afterEach, describe, expect, it } from 'vitest';
import type { Disposable } from '@/core/Disposable.ts';
import type { Chat, Entry } from '@/model/types.ts';
import { channelUpdate } from '@/components/ChannelDialog.ts';
import { keeps } from '@/components/ConversationView.ts';
import { filtersOf, rangeRequest } from '@/components/LogsPane.ts';
import { isForMe } from '@/components/NeedsYou.ts';
import { ReadTracker, type ObserverLike } from '@/components/ReadTracker.ts';
import { ChatPage, route, streamOf } from '@/pages/ChatPage.ts';
import { HomePage } from '@/pages/HomePage.ts';
import { FakeClock } from '@/testing/FakeClock.ts';
import { FakeFrames } from '@/testing/FakeFrames.ts';
import { fixture, loadPage } from '@/testing/pages.ts';
import { fakeServices, flush } from '@/testing/services.ts';

const message = fixture('entry_message') as Entry;
const question = fixture('entry_question') as Entry;
const marker = fixture('entry_marker') as Entry;
const line = fixture('entry_line') as Entry;

let running: Disposable | null = null;
afterEach(() => {
  running?.dispose();
  running = null;
});

describe('routing on a chat page', () => {
  it('sends messages to the conversation and stream entries to the logs, markers to both', () => {
    expect(route(message, 'chan:dut1', 'chan:dut1', 'markers')).toEqual({ conversation: true, logs: false, event: false });
    expect(route({ ...message, chat: 'chan:other' }, 'chan:dut1', 'chan:dut1', 'markers').conversation).toBe(false);
    expect(route(marker, 'chan:dut1', 'chan:dut1', 'markers')).toEqual({ conversation: true, logs: true, event: true });
    expect(route(line, 'chan:dut1', 'chan:dut1', 'markers')).toEqual({ conversation: false, logs: true, event: false });
    expect(route(line, 'chan:dut1', 'chan:dut1', 'errors')).toEqual({ conversation: true, logs: true, event: true });
    expect(route(marker, 'chan:dut1', 'chan:dut1', 'none').conversation).toBe(false);
    expect(route({ ...marker, stream: 'chan:dut1' }, 'chan:dut1', 'chan:dut1', 'none')).toEqual({ conversation: true, logs: false, event: false });
    expect(streamOf('dm:codex', 'alice')).toBe('dm:alice~codex');
    expect(streamOf('dm:alice', 'zed')).toBe('dm:alice~zed');
  });

  it('filters new rows like the server chips', () => {
    expect(keeps('questions', 'me', question)).toBe(true);
    expect(keeps('questions', 'me', message)).toBe(false);
    expect(keeps('images', 'me', message)).toBe(true);
    expect(keeps('mine', 'claude-laptop', message)).toBe(true);
    expect(keeps('events', 'me', { ...marker, event: true })).toBe(true);
    expect(keeps('', 'me', line)).toBe(true);
  });
});

describe('ChatPage', () => {
  it('shows a marker of a bound stream in the logs pane and as an event row', async () => {
    loadPage('chat');
    const { services, sources } = fakeServices((job) => ({ ...message, seq: 900, chat: job.chat, text: job.text, segments: [{ t: 'text', v: job.text }], sender: { name: 'admin', kind: 'human' } }));
    const frames = new FakeFrames();
    running = new ChatPage(document, services, frames).start();
    const feed = sources[0];
    expect(feed?.url).toMatch(/^\/api\/events\?chat=chan%3Adut1&since_seq=\d+$/);
    feed?.emit('entry', { ...marker, seq: 500, text: 'test 42 starts' });
    feed?.emit('entry', { ...line, seq: 501, text: 'a line after it' });
    frames.run();
    const conv = document.querySelector('[data-component="ConversationView"]');
    const logs = document.querySelector('[data-component="LogsPane"] [data-slot="list"]');
    expect(conv?.lastElementChild?.className).toBe('event-row');
    expect(conv?.lastElementChild?.querySelector('[data-slot="text"]')?.textContent).toBe('test 42 starts');
    expect(Array.from(logs?.children ?? []).slice(-2).map((r) => r.className)).toEqual(['marker', 'line']);
    // A message of this chat joins the conversation once.
    feed?.emit('entry', { ...message, seq: 502, chat: 'chan:dut1' });
    feed?.emit('entry', { ...message, seq: 502, chat: 'chan:dut1' });
    expect(conv?.querySelectorAll('[data-seq="502"]')).toHaveLength(1);
    // The composer sends through the queue.
    const text = document.querySelector<HTMLTextAreaElement>('form.composer textarea');
    if (text !== null) text.value = 'on my way';
    document.querySelector<HTMLFormElement>('form.composer')?.dispatchEvent(new Event('submit', { cancelable: true }));
    await flush();
    expect(conv?.querySelector('[data-seq="900"] [data-slot="body"]')?.textContent).toBe('on my way');
    // Receipts and answers.
    feed?.emit('seen', { seq: 502, by: 'guillaume' });
    expect(conv?.querySelector('[data-seq="502"] [data-slot="seen-by"]')?.textContent).toBe('seen by guillaume');
    const q = document.querySelector<HTMLElement>('.conversation .question[data-open]');
    const seq = Number(q?.dataset['seq']);
    feed?.emit('answered', { seq, answer: { by: 'admin', choice: 'done', seq: 999 } });
    expect(q?.hasAttribute('data-open')).toBe(false);
    expect(q?.querySelector('[data-slot="answer"]')?.textContent).toBe('answered by admin: done');
  });

  it('answers a question with its buttons through the queue', async () => {
    loadPage('chat');
    const { services, uploads } = fakeServices((job) => ({ ...message, seq: 901, chat: job.chat, text: job.choice ?? '' }));
    running = new ChatPage(document, services, new FakeFrames()).start();
    const card = document.querySelector<HTMLElement>('.conversation .question[data-open]');
    const form = card?.querySelector('form');
    const done = card?.querySelector<HTMLButtonElement>('button[value="done"]');
    const submit = new Event('submit', { cancelable: true, bubbles: true }) as SubmitEvent;
    Object.defineProperty(submit, 'submitter', { value: done });
    form?.dispatchEvent(submit);
    await flush();
    expect(submit.defaultPrevented).toBe(true);
    expect(uploads.sent[0]).toMatchObject({ chat: 'chan:dut1', choice: 'done', replyTo: Number(card?.dataset['seq']) });
  });
});

describe('HomePage', () => {
  it('lists a new question in Needs you and updates the badges and title', () => {
    loadPage('home');
    const { services, sources } = fakeServices(() => message);
    running = new HomePage(document, services).start();
    const feed = sources[0];
    expect(feed?.url).toBe('/api/events?scope=home');
    const q = { ...question, seq: 777, recipient: '@humans', question: { choices: ['yes'], open: true }, sender: { name: 'claude-laptop', kind: 'agent' } };
    feed?.emit('entry', q);
    const needs = document.querySelector<HTMLElement>('[data-component="NeedsYou"]');
    expect(needs?.hidden).toBe(false);
    expect(needs?.querySelector('[data-slot="questions"] .question')?.getAttribute('data-seq')).toBe('777');
    feed?.emit('counts', { open_questions: 3, unread_direct: 1, sources_down: 0, chats: [{ id: 'chan:dut1', unread: 5, open_questions: 2 }] });
    expect(document.title.startsWith('(4) ')).toBe(true);
    const row = document.querySelector('.chat-rows a.chat-row[data-chat="chan:dut1"]');
    expect(row?.querySelector('[data-slot="unread"]')?.textContent).toBe('5');
    expect(row?.querySelector('[data-slot="questions"]')?.textContent).toBe('2');
    feed?.emit('answered', { seq: 777, answer: { by: 'admin', seq: 778 } });
    expect(needs?.querySelector('[data-seq="777"]')).toBeNull();
    feed?.emit('chat', { ...(fixture('chat_channel') as Chat), id: 'chan:dut1', name: 'dut1', muted: true });
    expect(row?.hasAttribute('data-muted')).toBe(true);
  });
});

describe('needs you, read state, logs, settings', () => {
  it('knows which questions are for the viewer', () => {
    const open = { ...question, question: { choices: [], open: true } };
    expect(isForMe({ ...open, recipient: '@humans' }, 'guillaume', true)).toBe(true);
    expect(isForMe({ ...open, recipient: '@humans' }, 'codex', false)).toBe(false);
    expect(isForMe({ ...open, recipient: 'guillaume' }, 'guillaume', true)).toBe(true);
    expect(isForMe({ ...question, recipient: '@humans' }, 'guillaume', true)).toBe(false);
  });

  it('acks a row once it was visible for a second, in one request', () => {
    const clock = new FakeClock();
    let cb: ((e: readonly { target: Element; isIntersecting: boolean }[]) => void) | null = null;
    const observer: ObserverLike = { observe: () => {}, unobserve: () => {}, disconnect: () => {} };
    const acked: number[][] = [];
    const t = new ReadTracker((c) => ((cb = c), observer), clock, (seqs) => (acked.push(seqs), Promise.resolve()));
    const rows = [1, 2, 3].map(() => document.createElement('div'));
    rows.forEach((r, i) => {
      r.dataset['seq'] = String(i + 1);
      t.watch(r);
    });
    cb!(rows.map((target) => ({ target, isIntersecting: true })));
    clock.advance(500);
    cb!([{ target: rows[2] as Element, isIntersecting: false }]);
    clock.advance(500);
    clock.advance(400);
    expect(acked).toEqual([[1, 2]]);
    t.dispose();
  });

  it('carries the logs filters into an attached range', () => {
    const f = filtersOf('channel=dut1&sev=3&re=usb');
    expect(rangeRequest(20, 10, ['serial:dut1', 'serial:dut1'], f)).toEqual({ stream: 'serial:dut1', from_seq: 10, to_seq: 20, grep: 'usb', min_severity: 3 });
    expect(rangeRequest(10, 20, ['serial:dut1', 'netconsole:dut1'], f)).toEqual({ channel: 'dut1', from_seq: 10, to_seq: 20, grep: 'usb', min_severity: 3 });
    expect(filtersOf('channel=dut1')).toEqual({ channel: 'dut1' });
  });

  it('sends only what the settings form changed', () => {
    const chat = fixture('chat_channel') as Chat;
    expect(channelUpdate(chat, { title: chat.title ?? '', topic: 'new', surface: 'errors', rename: '', archived: false }, ['serial:dut2'], ['syslog:dut1*'])).toEqual({
      topic: 'new',
      surface: 'errors',
      add_patterns: ['serial:dut2'],
      remove_patterns: ['syslog:dut1*'],
    });
    expect(channelUpdate(chat, { title: chat.title ?? '', topic: chat.topic ?? '', surface: chat.surface, rename: ' run-43 ', archived: null }, [], [])).toEqual({ rename: 'run-43' });
  });
});
