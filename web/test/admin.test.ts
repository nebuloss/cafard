import { describe, expect, it } from 'vitest';
import { canonical, fromHash, navLevel, placeOf, trailOf } from '../src/admin/route';

describe('canonical', () => {
  it('keeps the paths that name a page', () => {
    for (const p of ['/', '/members', '/members/dev-code', '/members/dev-code/access', '/projects/be98/files', '/audit', '/settings']) {
      expect(canonical(p)).toBe(p);
    }
  });

  it('moves what names no page to the level above', () => {
    expect(canonical('')).toBe('/');
    expect(canonical('/overview')).toBe('/');
    expect(canonical('/members/')).toBe('/members');
    expect(canonical('/members/dev-code/nope')).toBe('/members/dev-code');
    expect(canonical('/projects/be98/access')).toBe('/projects/be98');
    expect(canonical('/audit/old')).toBe('/audit');
    expect(canonical('/storage')).toBe('/');
  });

  it('encodes a name once, and reads it back decoded', () => {
    expect(canonical('/members/ines.d')).toBe('/members/ines.d');
    expect(canonical('/members/a%20b')).toBe('/members/a%20b');
    expect(placeOf('/members/a%20b/projects')).toEqual({ tab: 'members', name: 'a b', page: 'projects' });
    expect(placeOf('/members/%E0')).toEqual({ tab: 'members', name: '%E0', page: '' });
  });
});

describe('navLevel', () => {
  it('lists the sections at the root, the page marked', () => {
    const l = navLevel('/projects');
    expect(l.back).toBeUndefined();
    expect(l.items.map((i) => i.href)).toEqual(['/', '/members', '/projects', '/audit', '/settings']);
    expect(l.current).toBe('/projects');
  });

  it("lists an item's pages, with the way back to its list", () => {
    const l = navLevel('/members/dev-code/access');
    expect(l.back?.href).toBe('/members');
    expect(l.items.map((i) => i.href)).toEqual(['/members/dev-code', '/members/dev-code/projects', '/members/dev-code/access', '/members/dev-code/advanced']);
    expect(l.current).toBe('/members/dev-code/access');
    expect(navLevel('/members/dev-code').current).toBe('/members/dev-code');
  });
});

describe('trailOf', () => {
  const name = (_: string, n: string) => (n === 'guillaume' ? 'Guillaume' : n);
  it('starts at the overview and names every level once', () => {
    expect(trailOf('/', name).map((c) => c.label)).toEqual(['Overview']);
    expect(trailOf('/audit', name).map((c) => c.label)).toEqual(['Overview', 'Audit log']);
    expect(trailOf('/members/guillaume/projects', name).map((c) => [c.label, c.href])).toEqual([
      ['Overview', '/'], ['Members', '/members'], ['Guillaume', '/members/guillaume'], ['Projects', '/members/guillaume/projects'],
    ]);
  });
});

describe('fromHash', () => {
  it('sends the links from before the paths where they meant', () => {
    expect(fromHash('#members')).toBe('/members');
    expect(fromHash('#storage')).toBe('/');
    expect(fromHash('#config')).toBe('/settings');
    expect(fromHash('')).toBeNull();
    expect(fromHash('#whatever')).toBeNull();
  });
});
