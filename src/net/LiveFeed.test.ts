import { describe, expect, it } from 'vitest';
import { Backoff } from '@/core/Backoff.ts';
import { LiveFeed } from '@/net/LiveFeed.ts';
import type { Entry } from '@/model/types.ts';
import { FakeClock } from '@/testing/FakeClock.ts';
import { FakeSource } from '@/testing/FakeSource.ts';
import { fixture } from '@/testing/pages.ts';

function setup(): { feed: LiveFeed; sources: FakeSource[]; clock: FakeClock } {
  const sources: FakeSource[] = [];
  const clock = new FakeClock();
  const feed = new LiveFeed(
    (url) => {
      const s = new FakeSource(url);
      sources.push(s);
      return s;
    },
    clock,
    new Backoff({ baseMs: 1000, maxMs: 30000, random: () => 0.5 }),
    '/api/events?streams=serial%3A*',
  );
  return { feed, sources, clock };
}

const line = (seq: number): Entry => ({ ...(fixture('entry_line') as Entry), seq });

describe('LiveFeed', () => {
  it('goes live, delivers parsed entries and resumes after the last one', () => {
    const { feed, sources, clock } = setup();
    const got: number[] = [];
    feed.events.on('entry', (e) => got.push(e.seq));
    feed.start(100);
    expect(sources[0]?.url).toBe('/api/events?streams=serial%3A*&since_seq=100');
    expect(feed.state.value.state).toBe('connecting');
    sources[0]?.open();
    expect(feed.state.value.state).toBe('live');
    sources[0]?.emit('entry', line(100));
    sources[0]?.emit('entry', line(101));
    sources[0]?.emit('entry', line(101)); // a replay overlap
    sources[0]?.emit('entry', { seq: 'garbage' });
    expect(got).toEqual([100, 101]);

    sources[0]?.fail();
    expect(sources[0]?.closed).toBe(true);
    expect(feed.state.value).toEqual({ state: 'retrying', attempt: 1, inMs: 750 });
    clock.advance(749);
    expect(sources).toHaveLength(1);
    clock.advance(1);
    expect(sources[1]?.url).toBe('/api/events?streams=serial%3A*&since_seq=102');
    sources[1]?.fail();
    expect(feed.state.value).toMatchObject({ state: 'retrying', attempt: 2, inMs: 1500 });
    clock.advance(1500);
    sources[2]?.open();
    sources[1]?.emit('entry', line(500)); // a closed source is ignored
    expect(got).toEqual([100, 101]);
    feed.dispose();
    expect(sources[2]?.closed).toBe(true);
    expect(feed.state.value.state).toBe('closed');
  });

  it('stops on reset and restarts where the page says', () => {
    const { feed, sources, clock } = setup();
    const resets: string[] = [];
    feed.events.on('reset', (r) => resets.push(r.reason));
    feed.start();
    expect(sources[0]?.url).toBe('/api/events?streams=serial%3A*');
    sources[0]?.open();
    sources[0]?.emit('reset', { reason: 'lagged' });
    expect(resets).toEqual(['lagged']);
    expect(sources[0]?.closed).toBe(true);
    clock.advance(60000);
    expect(sources).toHaveLength(1);
    feed.restart(9000);
    expect(sources[1]?.url).toBe('/api/events?streams=serial%3A*&since_seq=9000');
  });

  it('passes counts and stream_status on', () => {
    const { feed, sources } = setup();
    const downs: number[] = [];
    const ids: string[] = [];
    feed.events.on('counts', (c) => downs.push(c.sources_down));
    feed.events.on('stream_status', (s) => ids.push(s.id));
    feed.start();
    sources[0]?.emit('counts', fixture('counts'));
    sources[0]?.emit('stream_status', fixture('stream'));
    expect(downs).toEqual([0]);
    expect(ids).toEqual(['serial:lab/dut1']);
  });
});
