import { describe, expect, it } from 'vitest';
import { AuditFilter, valueLabel } from '../src/admin/audit-filter';

describe('AuditFilter', () => {
  it('reads its values from the query string, and writes them back in order', () => {
    const f = AuditFilter.fromSearch('?source=cli&actor=admin&x=1&actor=admin&kind=links');
    expect(f.chosen('actor')).toEqual(['admin']);
    expect(f.query()).toBe('actor=admin&kind=links&source=cli');
    expect(f.href()).toBe('/audit?actor=admin&kind=links&source=cli');
    expect(AuditFilter.fromSearch('').href()).toBe('/audit');
    expect(AuditFilter.fromSearch('?target=a%20b').chosen('target')).toEqual(['a b']);
  });

  it('toggles, narrows to one, and clears a column', () => {
    const f = AuditFilter.empty().toggled('actor', 'admin').toggled('actor', 'camille');
    expect(f.chosen('actor')).toEqual(['admin', 'camille']);
    expect(f.toggled('actor', 'admin').chosen('actor')).toEqual(['camille']);
    expect(f.only('actor', 'dev-code').chosen('actor')).toEqual(['dev-code']);
    expect(f.cleared('actor').empty).toBe(true);
    expect(f.pinned()).toEqual([
      { field: 'actor', value: 'admin' },
      { field: 'actor', value: 'camille' },
    ]);
  });

  it('names kinds and sources as people read them', () => {
    expect(valueLabel('kind', 'links')).toBe('Links & tokens');
    expect(valueLabel('source', 'cli')).toBe('Box (cli)');
    expect(valueLabel('actor', 'camille')).toBe('camille');
    expect(valueLabel('target', '')).toBe('none');
  });
});
