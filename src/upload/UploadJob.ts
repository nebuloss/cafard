import { Observable } from '@/core/Observable.ts';
import type { Entry, LogRequest } from '@/model/types.ts';

/** A file to send, with the name the server quotes back in errors. */
export interface JobFile {
  readonly name: string;
  readonly blob: Blob;
}

/** What a job sends: the fields of POST /api/messages (CONTRACTS.md 3.4). */
export interface JobData {
  readonly id: string;
  readonly chat: string;
  readonly text: string;
  readonly replyTo?: number;
  readonly choice?: string;
  readonly logs?: readonly LogRequest[];
  readonly staged?: string;
  readonly files: readonly JobFile[];
  /** Files already processed are not resized again on a retry. */
  readonly processed: boolean;
  readonly keepOriginal: boolean;
  readonly createdAt: number;
}

export type JobState =
  | { readonly state: 'queued' }
  | { readonly state: 'processing' }
  | { readonly state: 'sending'; readonly progress: number }
  | { readonly state: 'done'; readonly entry: Entry }
  | { readonly state: 'failed'; readonly message: string; readonly retryable: boolean; readonly retryInMs?: number };

type Name = JobState['state'];

/** Which state may follow which. A failed job is queued again by a retry. */
const NEXT: Readonly<Record<Name, readonly Name[]>> = {
  queued: ['processing', 'sending', 'failed'],
  processing: ['sending', 'failed'],
  sending: ['sending', 'done', 'failed'],
  done: [],
  failed: ['queued'],
};

/**
 * Command (PLAN 9.4): one message to send, with its own state machine,
 * queued -> processing -> sending(progress) -> done | failed. An illegal
 * transition is a bug, so it throws.
 */
export class UploadJob {
  readonly state: Observable<JobState>;

  constructor(private current: JobData) {
    this.state = new Observable<JobState>({ state: 'queued' }, (a, b) => JSON.stringify(a) === JSON.stringify(b));
  }

  get id(): string {
    return this.current.id;
  }

  get data(): JobData {
    return this.current;
  }

  /** Replaces the files once processed, so a retry sends the same bytes. */
  setFiles(files: readonly JobFile[]): void {
    this.current = { ...this.current, files, processed: true };
  }

  private move(next: JobState): void {
    const from = this.state.value.state;
    if (!NEXT[from].includes(next.state)) throw new Error(`upload ${this.id}: ${from} -> ${next.state}`);
    this.state.set(next);
  }

  processing(): void {
    this.move({ state: 'processing' });
  }

  sending(progress: number): void {
    this.move({ state: 'sending', progress: Math.max(0, Math.min(1, progress)) });
  }

  done(entry: Entry): void {
    this.move({ state: 'done', entry });
  }

  fail(message: string, retryable: boolean, retryInMs?: number): void {
    this.move(retryInMs === undefined ? { state: 'failed', message, retryable } : { state: 'failed', message, retryable, retryInMs });
  }

  retry(): void {
    this.move({ state: 'queued' });
  }
}
