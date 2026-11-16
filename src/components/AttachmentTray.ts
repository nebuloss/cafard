import { Observable } from '@/core/Observable.ts';
import type { LogRequest } from '@/model/types.ts';
import { attachmentChip } from '@/render/renderers/AttachmentChipRenderer.ts';
import type { SlotTemplate } from '@/render/SlotTemplate.ts';
import { Component } from '@/components/Component.ts';
import { linesText } from '@/render/format.ts';

/** A log slice waiting in the composer, with what its chip says. */
export interface PendingLog {
  readonly request: LogRequest;
  readonly label: string;
}

/** What the tray holds. */
export interface TrayItems {
  readonly files: readonly File[];
  readonly logs: readonly PendingLog[];
}

/** The label of a log chip: stream, line count, title (PLAN 9.3). */
export function logLabel(request: LogRequest, lines?: number): string {
  const where = request.stream ?? `#${request.channel ?? ''}`;
  const count = lines === undefined ? (request.to_seq === undefined ? 'from now on' : '') : linesText(lines, request.to_seq === undefined);
  return [where, count, request.title].filter((s) => s !== undefined && s !== '').join(' · ');
}

/**
 * The attachments waiting in a composer: photos and log chips, each with
 * a remove button, built from tpl-attachment-chip.
 */
export class AttachmentTray extends Component {
  readonly items = new Observable<TrayItems>({ files: [], logs: [] });

  constructor(
    root: HTMLElement,
    private readonly chip: SlotTemplate | null,
    private readonly maxFiles: number,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.listen(this.root, 'click', (event) => {
      const button = (event.target as Element | null)?.closest('[data-slot="remove"]');
      const chip = button?.closest<HTMLElement>('[data-kind]');
      if (chip === null || chip === undefined) return;
      const index = Number(chip.dataset['index']);
      const { files, logs } = this.items.value;
      if (chip.dataset['kind'] === 'image') this.items.set({ files: files.filter((_, i) => i !== index), logs });
      else this.items.set({ files, logs: logs.filter((_, i) => i !== index) });
    });
    this.bag.add(this.items.subscribe(() => this.render()));
  }

  /** Adds photos; past the server's limit the rest are dropped, and the count says so. */
  addFiles(files: readonly File[]): number {
    const images = files.filter((f) => f.type === '' || f.type.startsWith('image/'));
    const { files: current, logs } = this.items.value;
    const room = Math.max(0, this.maxFiles - current.length);
    this.items.set({ files: [...current, ...images.slice(0, room)], logs });
    return Math.min(room, images.length);
  }

  addLog(log: PendingLog): void {
    const { files, logs } = this.items.value;
    this.items.set({ files, logs: [...logs, log].slice(0, 5) });
  }

  clear(): void {
    this.items.set({ files: [], logs: [] });
  }

  get empty(): boolean {
    const { files, logs } = this.items.value;
    return files.length === 0 && logs.length === 0;
  }

  private render(): void {
    const chip = this.chip;
    if (chip === null) return;
    const { files, logs } = this.items.value;
    this.root.replaceChildren(
      ...files.map((f, i) => attachmentChip(chip, 'image', i, f.name === '' ? 'photo' : f.name)),
      ...logs.map((l, i) => attachmentChip(chip, 'log', i, l.label)),
    );
  }
}
