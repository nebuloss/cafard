import type { Entry } from '@/model/types.ts';

/** Turns one entry into its row. */
export interface EntryRenderer {
  render(entry: Entry): HTMLElement;
}
