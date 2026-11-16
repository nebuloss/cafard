import type { SlotTemplate } from '@/render/SlotTemplate.ts';

export const ATTACHMENT_CHIP_ATTRIBUTES = ['data-kind', 'data-index'] as const;
export const UPLOAD_STATUS_ATTRIBUTES = ['data-state', 'value'] as const;

/** A chip of the attachment tray (tpl-attachment-chip). */
export function attachmentChip(template: SlotTemplate, kind: 'image' | 'log', index: number, label: string): HTMLElement {
  return template.clone().attr({ 'data-kind': kind, 'data-index': index }).text('label', label).root;
}
