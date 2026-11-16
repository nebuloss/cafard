import { parseEntry, parseError } from '@/model/parse.ts';
import type { Entry } from '@/model/types.ts';
import { ApiError } from '@/net/ApiError.ts';
import type { JobData } from '@/upload/UploadJob.ts';

/** Sends one message, reporting upload progress from 0 to 1. */
export interface UploadClient {
  send(job: JobData, onProgress: (fraction: number) => void): Promise<Entry>;
}

/** The multipart fields of POST /api/messages (CONTRACTS.md 3.4). */
export function formOf(job: JobData): FormData {
  const form = new FormData();
  form.set('chat', job.chat);
  if (job.text !== '') form.set('text', job.text);
  if (job.replyTo !== undefined) form.set('reply_to', String(job.replyTo));
  if (job.choice !== undefined) form.set('choice', job.choice);
  if (job.logs !== undefined && job.logs.length > 0) form.set('logs', JSON.stringify(job.logs));
  if (job.staged !== undefined) form.set('staged', job.staged);
  for (const f of job.files) form.append('files[]', f.blob, f.name);
  return form;
}

/** A minimal XMLHttpRequest, so a test can fake it. */
export interface XhrLike {
  open(method: string, url: string): void;
  setRequestHeader(name: string, value: string): void;
  send(body: FormData): void;
  readonly status: number;
  readonly responseText: string;
  readonly upload: { onprogress: ((e: { loaded: number; total: number; lengthComputable: boolean }) => void) | null };
  onload: (() => void) | null;
  onerror: (() => void) | null;
  ontimeout: (() => void) | null;
}

/**
 * XMLHttpRequest, because fetch reports no upload progress. Like
 * HttpClient it sends X-Cafard: 1 so the route answers JSON, and turns
 * failures into ApiErrors carrying the server's sentence.
 */
export class XhrUploadClient implements UploadClient {
  constructor(private readonly create: () => XhrLike = () => new XMLHttpRequest() as unknown as XhrLike) {}

  send(job: JobData, onProgress: (fraction: number) => void): Promise<Entry> {
    return new Promise((resolve, reject) => {
      const xhr = this.create();
      xhr.open('POST', '/api/messages');
      xhr.setRequestHeader('X-Cafard', '1');
      xhr.setRequestHeader('Accept', 'application/json');
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && e.total > 0) onProgress(e.loaded / e.total);
      };
      xhr.onerror = () => reject(new ApiError('offline', 'The server cannot be reached.', 0));
      xhr.ontimeout = xhr.onerror;
      xhr.onload = () => {
        let raw: unknown = null;
        try {
          raw = JSON.parse(xhr.responseText);
        } catch {
          raw = null;
        }
        if (xhr.status === 201 || xhr.status === 200) {
          try {
            resolve(parseEntry(raw));
          } catch (error) {
            reject(error);
          }
          return;
        }
        try {
          const e = parseError(raw);
          reject(new ApiError(e.code, e.message, xhr.status, e.retry_after_s));
        } catch {
          reject(new ApiError('http_error', `The server answered ${xhr.status}.`, xhr.status));
        }
      };
      xhr.send(formOf(job));
    });
  }
}
