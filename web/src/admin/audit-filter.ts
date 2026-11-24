/**
 * The audit log's filter, as claudication's request log keeps its own
 * (web/src/ui/features/requests/filter.ts there): for each column, the
 * values an entry must have one of. It lives in the URL's query string, so
 * a filtered log is a link, and Back undoes a narrowing.
 *
 * Pure, and tested under node.
 */

import { t } from '@shared/i18n';

export const AUDIT_FIELDS = ['actor', 'kind', 'target', 'source'] as const;
export type AuditField = (typeof AUDIT_FIELDS)[number];

/** What a column is called where someone reads it. */
export function fieldLabel(field: AuditField): string {
  switch (field) {
    case 'actor':
      return t('Who');
    case 'kind':
      return t('Kind');
    case 'target':
      return t('Target');
    case 'source':
      return t('From');
  }
}

/**
 * How a kind or a source reads; any other value reads as itself. The keys
 * are the values the server stores, and stay English; only what is shown
 * for them is translated.
 */
function valueLabels(field: AuditField): Record<string, string> | undefined {
  switch (field) {
    case 'kind':
      return { members: t('Members'), projects: t('Projects'), links: t('Links & tokens'), 'sign-ins': t('Sign-ins'), settings: t('Settings') };
    case 'source':
      return { admin: t('Admin UI'), chat: t('Chat'), cli: t('Box (cli)'), join: t('Join link'), mcp: 'MCP', api: 'API', 'upload link': t('Upload link') };
    default:
      return undefined;
  }
}

export function valueLabel(field: AuditField, value: string): string {
  if (value === '') return t('none');
  return valueLabels(field)?.[value] ?? value;
}

export class AuditFilter {
  private constructor(private readonly values: Readonly<Record<AuditField, readonly string[]>>) {}

  static empty(): AuditFilter {
    return new AuditFilter({ actor: [], kind: [], target: [], source: [] });
  }

  /** The filter a query string holds; anything else in it is ignored. */
  static fromSearch(search: string): AuditFilter {
    const q = new URLSearchParams(search);
    const v = {} as Record<AuditField, string[]>;
    for (const f of AUDIT_FIELDS) v[f] = [...new Set(q.getAll(f))];
    return new AuditFilter(v);
  }

  /** The query string, in a fixed order, without the "?"; empty for no filter. */
  query(): string {
    const q = new URLSearchParams();
    for (const f of AUDIT_FIELDS) for (const v of this.values[f]) q.append(f, v);
    return q.toString();
  }

  /** The path of the log narrowed this way. */
  href(): string {
    const q = this.query();
    return q ? `/audit?${q}` : '/audit';
  }

  chosen(field: AuditField): readonly string[] {
    return this.values[field];
  }

  has(field: AuditField, value: string): boolean {
    return this.values[field].includes(value);
  }

  narrows(field: AuditField): boolean {
    return this.values[field].length > 0;
  }

  get empty(): boolean {
    return AUDIT_FIELDS.every((f) => !this.narrows(f));
  }

  private with(field: AuditField, list: string[]): AuditFilter {
    return new AuditFilter({ ...this.values, [field]: list });
  }

  /** The value added to its column's choice, or taken out of it. */
  toggled(field: AuditField, value: string): AuditFilter {
    return this.with(field, this.has(field, value) ? this.values[field].filter((v) => v !== value) : [...this.values[field], value]);
  }

  /** That value alone in its column: what clicking it in a row does. */
  only(field: AuditField, value: string): AuditFilter {
    return this.with(field, [value]);
  }

  cleared(field: AuditField): AuditFilter {
    return this.with(field, []);
  }

  /** Every value chosen, as the chips that say so. */
  pinned(): { field: AuditField; value: string }[] {
    return AUDIT_FIELDS.flatMap((field) => this.values[field].map((value) => ({ field, value })));
  }
}
