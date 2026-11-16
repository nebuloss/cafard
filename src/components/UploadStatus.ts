import { DisposableBag } from '@/core/Disposable.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';
import { UPLOAD_STATUS_ATTRIBUTES } from '@/render/renderers/AttachmentChipRenderer.ts';
import type { UploadQueue } from '@/upload/UploadQueue.ts';
import type { JobState, UploadJob } from '@/upload/UploadJob.ts';
import { Component } from '@/components/Component.ts';

export { UPLOAD_STATUS_ATTRIBUTES };

/** What a job's row says in each state. */
export function statusLabel(job: UploadJob, s: JobState): string {
  const files = job.data.files.length;
  const what = files === 0 ? 'Message' : files === 1 ? '1 photo' : `${files} photos`;
  switch (s.state) {
    case 'queued':
      return `${what}: waiting to send`;
    case 'processing':
      return `${what}: preparing`;
    case 'sending':
      return `${what}: sending ${Math.round(s.progress * 100)}%`;
    case 'done':
      return `${what}: sent`;
    case 'failed':
      // The server's sentence, as is.
      return s.retryable ? `${s.message} Retrying.` : s.message;
  }
}

/**
 * One row per message on its way (tpl-upload-status): progress while
 * sending, the server's sentence and a retry button when it failed. Jobs
 * of other chats are shown too: the queue is the browser's.
 */
export class UploadStatus extends Component {
  private readonly rows = new Map<string, { el: HTMLElement; bag: DisposableBag }>();

  constructor(
    root: HTMLElement,
    private readonly template: SlotTemplate | null,
    private readonly queue: UploadQueue,
    private readonly onlyChat?: string,
  ) {
    super(root);
  }

  protected bind(): void {
    for (const job of this.queue.pending) this.add(job);
    this.bag.add(this.queue.events.on('added', (job) => this.add(job)));
    this.bag.add(this.queue.events.on('removed', (job) => this.drop(job.id)));
    this.bag.defer(() => {
      for (const id of [...this.rows.keys()]) this.drop(id);
    });
  }

  private add(job: UploadJob): void {
    if (this.template === null || this.rows.has(job.id)) return;
    if (this.onlyChat !== undefined && job.data.chat !== this.onlyChat) return;
    const t = this.template.clone();
    const bag = new DisposableBag();
    const progress = t.slot('progress') as HTMLProgressElement;
    bag.listen(t.slot('retry'), 'click', () => this.queue.retry(job.id));
    bag.add(
      job.state.subscribe(
        (s) => {
          t.root.dataset['state'] = s.state;
          t.text('label', statusLabel(job, s));
          progress.value = s.state === 'sending' ? Math.round(s.progress * 100) : 0;
          (t.slot('retry') as HTMLButtonElement).hidden = s.state !== 'failed';
        },
        { immediate: true },
      ),
    );
    this.rows.set(job.id, { el: t.root, bag });
    this.root.append(t.root);
  }

  private drop(id: string): void {
    const row = this.rows.get(id);
    if (row === undefined) return;
    row.bag.dispose();
    row.el.remove();
    this.rows.delete(id);
  }
}
