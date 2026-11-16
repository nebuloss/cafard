import type { Backoff } from '@/core/Backoff.ts';
import type { Clock } from '@/core/Clock.ts';
import type { Disposable } from '@/core/Disposable.ts';
import { EventBus } from '@/core/EventBus.ts';
import type { Entry } from '@/model/types.ts';
import { ApiError } from '@/net/ApiError.ts';
import type { ImageProcessor } from '@/media/ImageProcessor.ts';
import type { JobRepository } from '@/upload/JobRepository.ts';
import type { UploadClient } from '@/upload/UploadClient.ts';
import { type JobData, type JobFile, type JobState, UploadJob } from '@/upload/UploadJob.ts';

export interface QueueEvents {
  /** A job was added (also those restored from the repository). */
  added: UploadJob;
  /** A job left the queue: sent, or removed by the person. */
  removed: UploadJob;
  sent: { readonly job: UploadJob; readonly entry: Entry };
}

/** The strategies a job's photos go through. */
export interface Processors {
  readonly resize: ImageProcessor;
  readonly keep: ImageProcessor;
}

/** Failures worth retrying on their own: the network, a busy server. */
function retryable(error: unknown): { retry: boolean; afterMs?: number } {
  if (!(error instanceof ApiError)) return { retry: false };
  if (error.code === 'offline' || error.status === 0) return { retry: true };
  if (error.status === 429 || error.status === 503) {
    return error.retryAfterS === undefined ? { retry: true } : { retry: true, afterMs: error.retryAfterS * 1000 };
  }
  return { retry: error.status >= 500 };
}

/**
 * Sends jobs one at a time, in order, so messages keep the order they were
 * written in. Network failures are retried with backoff; a refusal (400,
 * 413, 403) waits for the person to retry or remove it. Pending jobs are
 * kept in the repository until sent.
 */
export class UploadQueue implements Disposable {
  readonly events = new EventBus<QueueEvents>();
  private readonly jobs: UploadJob[] = [];
  private running = false;
  private timer: Disposable | null = null;
  private disposed = false;

  constructor(
    private readonly client: UploadClient,
    private readonly repository: JobRepository,
    private readonly processors: Processors,
    private readonly backoff: Backoff,
    private readonly clock: Clock,
  ) {}

  /** Jobs not sent yet, oldest first. */
  get pending(): readonly UploadJob[] {
    return this.jobs;
  }

  /** Loads what a previous page left unsent, then runs. */
  async restore(): Promise<void> {
    for (const data of await this.repository.all()) {
      if (this.jobs.some((j) => j.id === data.id)) continue;
      const job = new UploadJob(data);
      this.jobs.push(job);
      this.events.emit('added', job);
    }
    this.kick();
  }

  async add(data: JobData): Promise<UploadJob> {
    const job = new UploadJob(data);
    this.jobs.push(job);
    this.events.emit('added', job);
    try {
      await this.repository.put(data);
    } catch {
      // Not persisted: it is still sent while the page is open.
    }
    void this.run();
    return job;
  }

  /** The person asked: try a failed job again now. */
  retry(id: string): void {
    const job = this.jobs.find((j) => j.id === id);
    if (job?.state.value.state === 'failed') {
      job.retry();
      this.backoff.reset();
      this.kick();
    }
  }

  async remove(id: string): Promise<void> {
    const i = this.jobs.findIndex((j) => j.id === id);
    if (i < 0) return;
    const [job] = this.jobs.splice(i, 1);
    await this.repository.delete(id).catch(() => {});
    if (job !== undefined) this.events.emit('removed', job);
    void this.run();
  }

  /** Runs now: on start, after an add, and when the browser is online again. */
  kick(): void {
    this.timer?.dispose();
    this.timer = null;
    for (const job of this.jobs) {
      const s = job.state.value;
      if (s.state === 'failed' && s.retryable) job.retry();
    }
    void this.run();
  }

  dispose(): void {
    this.disposed = true;
    this.timer?.dispose();
  }

  private async run(): Promise<void> {
    if (this.running || this.disposed) return;
    this.running = true;
    try {
      for (;;) {
        // The oldest job not refused; a retryable failure holds the
        // ones behind it until its timer, which keeps the order.
        const job = this.jobs.find((j) => {
          const s = j.state.value;
          return !(s.state === 'failed' && !s.retryable);
        });
        if (job === undefined || job.state.value.state !== 'queued' || this.disposed) return;
        const ok = await this.attempt(job);
        if (!ok) {
          // Read again: attempt() moved it on.
          const s: JobState = job.state.value as JobState;
          if (s.state === 'failed' && s.retryable) {
            this.schedule(s.retryInMs);
            return;
          }
          // A refused job waits; the ones after it go on.
        }
      }
    } finally {
      this.running = false;
    }
  }

  private schedule(afterMs?: number): void {
    const wait = Math.max(afterMs ?? 0, this.backoff.next());
    this.timer = this.clock.setTimeout(() => {
      this.timer = null;
      this.kick();
    }, wait);
  }

  private async attempt(job: UploadJob): Promise<boolean> {
    try {
      if (!job.data.processed && job.data.files.length > 0) {
        job.processing();
        const strategy = job.data.keepOriginal ? this.processors.keep : this.processors.resize;
        const files: JobFile[] = [];
        for (const f of job.data.files) {
          const blob = await strategy.process(f.blob);
          files.push({ name: blob === f.blob ? f.name : jpegName(f.name), blob });
        }
        job.setFiles(files);
        await this.repository.put(job.data).catch(() => {});
      }
      job.sending(0);
      const entry = await this.client.send(job.data, (p) => job.sending(p));
      job.done(entry);
      this.backoff.reset();
      const i = this.jobs.indexOf(job);
      if (i >= 0) this.jobs.splice(i, 1);
      await this.repository.delete(job.id).catch(() => {});
      this.events.emit('sent', { job, entry });
      this.events.emit('removed', job);
      return true;
    } catch (error) {
      const r = retryable(error);
      const message = error instanceof ApiError ? error.message : 'The message could not be sent.';
      job.fail(message, r.retry, r.afterMs);
      return false;
    }
  }
}

function jpegName(name: string): string {
  const dot = name.lastIndexOf('.');
  return `${dot > 0 ? name.slice(0, dot) : name}.jpg`;
}

/** A job id: unique enough for one browser's queue. */
export function jobId(clock: Clock): string {
  return `job_${clock.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
}
