import { describe, expect, it } from 'vitest';
import { Backoff } from '@/core/Backoff.ts';
import type { Entry } from '@/model/types.ts';
import { ApiError } from '@/net/ApiError.ts';
import { KeepOriginal } from '@/media/KeepOriginal.ts';
import { type Bitmap, type CanvasEnv, fit, ResizeToJpeg } from '@/media/ResizeToJpeg.ts';
import { MemoryJobRepository } from '@/upload/JobRepository.ts';
import { formOf, XhrUploadClient, type UploadClient, type XhrLike } from '@/upload/UploadClient.ts';
import { type JobData, UploadJob } from '@/upload/UploadJob.ts';
import { UploadQueue } from '@/upload/UploadQueue.ts';
import { FakeClock } from '@/testing/FakeClock.ts';
import { fixture } from '@/testing/pages.ts';

const entry = fixture('entry_message') as Entry;

function job(id: string, extra: Partial<JobData> = {}): JobData {
  return { id, chat: 'chan:dut1', text: `text of ${id}`, files: [], processed: false, keepOriginal: false, createdAt: 0, ...extra };
}

const flush = async (): Promise<void> => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
};

describe('UploadJob', () => {
  it('moves queued -> processing -> sending -> done', () => {
    const j = new UploadJob(job('a'));
    const seen: string[] = [];
    j.state.subscribe((s) => seen.push(s.state === 'sending' ? `sending ${s.progress}` : s.state));
    j.processing();
    j.sending(0);
    j.sending(0.5);
    j.sending(2);
    j.done(entry);
    expect(seen).toEqual(['processing', 'sending 0', 'sending 0.5', 'sending 1', 'done']);
    expect(j.finished).toBe(true);
  });

  it('refuses transitions the machine does not have', () => {
    const j = new UploadJob(job('a'));
    expect(() => j.done(entry)).toThrow();
    j.fail('offline', true);
    expect(() => j.sending(0)).toThrow();
    j.retry();
    expect(j.state.value.state).toBe('queued');
    j.sending(0);
    j.done(entry);
    expect(() => j.retry()).toThrow();
  });
});

class FakeClient implements UploadClient {
  readonly sent: JobData[] = [];
  readonly plan: (Error | null)[] = [];
  send(data: JobData, onProgress: (f: number) => void): Promise<Entry> {
    const next = this.plan.shift() ?? null;
    if (next !== null) return Promise.reject(next);
    onProgress(0.5);
    onProgress(1);
    this.sent.push(data);
    return Promise.resolve({ ...entry, text: data.text });
  }
}

function queue(client: FakeClient, repo = new MemoryJobRepository(), clock = new FakeClock()) {
  const keep = new KeepOriginal();
  const resize = { process: (b: Blob) => Promise.resolve(new Blob([b], { type: 'image/jpeg' })) };
  return { q: new UploadQueue(client, repo, { resize, keep }, new Backoff({ baseMs: 1000, maxMs: 8000, random: () => 0.5 }), clock), repo, clock };
}

describe('UploadQueue', () => {
  it('sends in order and forgets what it sent', async () => {
    const client = new FakeClient();
    const { q, repo } = queue(client);
    const sent: string[] = [];
    q.events.on('sent', ({ entry: e }) => sent.push(e.text));
    await q.add(job('a', { createdAt: 1 }));
    await q.add(job('b', { createdAt: 2 }));
    await flush();
    expect(sent).toEqual(['text of a', 'text of b']);
    expect(await repo.all()).toEqual([]);
    expect(q.pending).toHaveLength(0);
  });

  it('retries a network failure with backoff, keeping later jobs behind it', async () => {
    const client = new FakeClient();
    client.plan.push(new ApiError('offline', 'The server cannot be reached.', 0), new ApiError('busy', 'Busy.', 503, 4));
    const { q, repo, clock } = queue(client);
    await q.add(job('a', { createdAt: 1 }));
    await q.add(job('b', { createdAt: 2 }));
    await flush();
    expect(client.sent).toHaveLength(0);
    expect(q.pending[0]?.state.value).toMatchObject({ state: 'failed', retryable: true });
    expect((await repo.all()).map((j) => j.id)).toEqual(['a', 'b']);
    // The first retry comes after the backoff's 750 ms.
    clock.advance(750);
    await flush();
    // The busy answer asked for 4 s, more than the backoff's next 1.5 s.
    expect(q.pending[0]?.state.value).toMatchObject({ state: 'failed', retryInMs: 4000 });
    clock.advance(3999);
    await flush();
    expect(client.sent).toHaveLength(0);
    clock.advance(1);
    await flush();
    expect(client.sent.map((j) => j.id)).toEqual(['a', 'b']);
  });

  it('leaves a refused job for the person and sends the next', async () => {
    const client = new FakeClient();
    client.plan.push(new ApiError('too_large', 'That is too big.', 413));
    const { q, clock } = queue(client);
    await q.add(job('a', { createdAt: 1 }));
    await q.add(job('b', { createdAt: 2 }));
    await flush();
    expect(client.sent.map((j) => j.id)).toEqual(['b']);
    expect(q.pending[0]?.state.value).toEqual({ state: 'failed', message: 'That is too big.', retryable: false });
    clock.advance(60_000);
    await flush();
    expect(client.sent).toHaveLength(1);
    q.retry('a');
    await flush();
    expect(client.sent.map((j) => j.id)).toEqual(['b', 'a']);
  });

  it('restores jobs left in the repository, processing photos once', async () => {
    const repo = new MemoryJobRepository();
    await repo.put(job('old', { files: [{ name: 'IMG_1.HEIC', blob: new Blob(['x'], { type: 'image/heic' }) }] }));
    const client = new FakeClient();
    client.plan.push(new ApiError('offline', 'The server cannot be reached.', 0));
    const { q, clock } = queue(client, repo);
    await q.restore();
    await flush();
    const stored = await repo.all();
    expect(stored[0]?.processed).toBe(true);
    expect(stored[0]?.files[0]?.name).toBe('IMG_1.jpg');
    clock.advance(1000);
    await flush();
    expect(client.sent[0]?.files[0]?.blob.type).toBe('image/jpeg');
  });

  it('keeps originals when asked', async () => {
    const client = new FakeClient();
    const { q } = queue(client);
    const blob = new Blob(['x'], { type: 'image/png' });
    await q.add(job('a', { keepOriginal: true, files: [{ name: 'a.png', blob }] }));
    await flush();
    expect(client.sent[0]?.files[0]).toEqual({ name: 'a.png', blob });
  });
});

describe('ResizeToJpeg', () => {
  const bitmap = (width: number, height: number): Bitmap & { closed: boolean } => ({ width, height, closed: false, close() { this.closed = true; } });

  it('fits the long edge to 2560 px and never enlarges', () => {
    expect(fit(4032, 3024, 2560)).toEqual({ width: 2560, height: 1920 });
    expect(fit(3024, 4032, 2560)).toEqual({ width: 1920, height: 2560 });
    expect(fit(800, 600, 2560)).toEqual({ width: 800, height: 600 });
  });

  it('decodes, resizes, encodes JPEG, and falls back when decoding fails', async () => {
    const b = bitmap(4032, 3024);
    const calls: string[] = [];
    const env: CanvasEnv = {
      decode: (file) => (file.type === 'image/heic-broken' ? Promise.reject(new Error('no')) : Promise.resolve(b)),
      encode: (_bm, w, h, q) => {
        calls.push(`${w}x${h}@${q}`);
        return Promise.resolve(new Blob(['j'], { type: 'image/jpeg' }));
      },
    };
    const r = new ResizeToJpeg(new KeepOriginal(), env);
    expect((await r.process(new Blob(['p'], { type: 'image/png' }))).type).toBe('image/jpeg');
    expect(calls).toEqual(['2560x1920@0.85']);
    expect(b.closed).toBe(true);
    const gif = new Blob(['g'], { type: 'image/gif' });
    expect(await r.process(gif)).toBe(gif);
    const broken = new Blob(['?'], { type: 'image/heic-broken' });
    expect(await r.process(broken)).toBe(broken);
  });
});

describe('XhrUploadClient', () => {
  function fakeXhr(status: number, body: string) {
    const x: XhrLike & { headers: Record<string, string>; body?: FormData } = {
      headers: {},
      status,
      responseText: body,
      upload: { onprogress: null },
      onload: null,
      onerror: null,
      ontimeout: null,
      open: () => {},
      setRequestHeader(name, value) {
        this.headers[name] = value;
      },
      send(form) {
        this.body = form;
        this.upload.onprogress?.({ loaded: 5, total: 10, lengthComputable: true });
        this.onload?.();
      },
    };
    return x;
  }

  it('posts the multipart fields with X-Cafard and reports progress', async () => {
    const x = fakeXhr(201, JSON.stringify(entry));
    const progress: number[] = [];
    const got = await new XhrUploadClient(() => x).send(
      job('a', { replyTo: 12, choice: 'yes', logs: [{ stream: 'serial:dut1', from_seq: 3, grep: 'usb' }], files: [{ name: 'a.jpg', blob: new Blob(['x']) }] }),
      (p) => progress.push(p),
    );
    expect(got.seq).toBe(entry.seq);
    expect(progress).toEqual([0.5]);
    expect(x.headers['X-Cafard']).toBe('1');
    expect(x.body?.get('chat')).toBe('chan:dut1');
    expect(x.body?.get('reply_to')).toBe('12');
    expect(x.body?.get('choice')).toBe('yes');
    expect(JSON.parse(String(x.body?.get('logs')))).toEqual([{ stream: 'serial:dut1', from_seq: 3, grep: 'usb' }]);
    expect(x.body?.getAll('files[]')).toHaveLength(1);
  });

  it('turns an error answer into the server sentence', async () => {
    const x = fakeXhr(429, JSON.stringify(fixture('error_rate_limited')));
    await expect(new XhrUploadClient(() => x).send(job('a'), () => {})).rejects.toMatchObject({ code: 'rate_limited', status: 429, retryAfterS: 12 });
  });

  it('builds no empty fields', () => {
    const f = formOf(job('a', { text: '' }));
    expect([...f.keys()]).toEqual(['chat']);
  });
});
