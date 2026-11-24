import type { Catalog } from '../../i18n';
import { admin } from './admin';
import { chat } from './chat';
import { shared } from './shared';

/**
 * French, by English text. One file per UI so each is kept beside what it
 * translates; both UIs load all three, which is a few kilobytes.
 */
export const fr: Catalog = { ...shared, ...admin, ...chat };
