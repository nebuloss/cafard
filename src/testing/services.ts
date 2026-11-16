import { Backoff } from '@/core/Backoff.ts';
import { MemoryStore } from '@/core/KeyValueStore.ts';
import type { Entry } from '@/model/types.ts';
import { CafardApi } from '@/net/CafardApi.ts';
import type { HttpClient, Method, RequestOptions } from '@/net/HttpClient.ts';
import { KeepOriginal } from '@/media/KeepOriginal.ts';
import { MemoryJobRepository } from '@/upload/JobRepository.ts';
import type { UploadClient } from '@/upload/UploadClient.ts';
import type { JobData } from '@/upload/UploadJob.ts';
import { UploadQueue } from '@/upload/UploadQueue.ts';
import { FakeClock } from '@/testing/FakeClock.ts';
import { FakeSource } from '@/testing/FakeSource.ts';
import type { Services } from '@/App.ts';

/** An HttpClient that records requests and answers from a table. */
export class FakeHttp implements HttpClient {
  readonly calls: { method: Method; path: string; body?: unknown; query?: unknown }[] = [];
  constructor(private readonly answer: (method: Method, path: string) => unknown = () => ({})) {}
  request<T>(method: Method, path: string, options: RequestOptions<T> = {}): Promise<T> {
    this.calls.push({ method, path, body: options.body, query: options.query });
    const raw = this.answer(method, path);
    return Promise.resolve(options.parse === undefined ? (raw as T) : options.parse(raw));
  }
}

/**
 * An upload client that sends nothing and remembers what it sent. Errors
 * pushed on plan fail the next sends, one each, and are not remembered.
 */
export class FakeUploads implements UploadClient {
  readonly sent: JobData[] = [];
  readonly plan: (Error | null)[] = [];
  constructor(private readonly reply: (job: JobData) => Entry) {}
  send(job: JobData, onProgress: (fraction: number) => void): Promise<Entry> {
    const failure = this.plan.shift() ?? null;
    if (failure !== null) return Promise.reject(failure);
    onProgress(0.5);
    onProgress(1);
    this.sent.push(job);
    return Promise.resolve(this.reply(job));
  }
}

/** Services for a page test: fake clock, memory storage, fake feed, fake HTTP and uploads. */
export function fakeServices(reply: (job: JobData) => Entry, answer?: (method: Method, path: string) => unknown) {
  const clock = new FakeClock(Date.parse('2026-11-16T12:00:00Z'));
  const sources: FakeSource[] = [];
  const http = new FakeHttp(answer);
  const uploads = new FakeUploads(reply);
  const keep = new KeepOriginal();
  const services: Services = {
    clock,
    storage: new MemoryStore(),
    http,
    api: new CafardApi(http),
    backoff: () => new Backoff({ baseMs: 1000, maxMs: 30000 }),
    uploads: new UploadQueue(uploads, new MemoryJobRepository(), { resize: keep, keep }, new Backoff({ baseMs: 1000, maxMs: 30000 }), clock),
    eventSources: (url) => {
      const source = new FakeSource(url);
      sources.push(source);
      return source;
    },
  };
  return { services, clock, sources, http, uploads };
}

export const flush = async (): Promise<void> => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
};
