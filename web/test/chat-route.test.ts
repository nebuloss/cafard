import { describe, expect, it } from 'vitest';
import { fromHash, pathOf, placeOf } from '../src/chat/route';

describe('chat paths', () => {
  it('reads a conversation, a new one, and the list', () => {
    expect(placeOf('/be98/wifi-drops')).toEqual({ kind: 'conversation', project: 'be98', name: 'wifi-drops' });
    expect(placeOf('/be98/wifi-drops/')).toEqual({ kind: 'conversation', project: 'be98', name: 'wifi-drops' });
    expect(placeOf('/new')).toEqual({ kind: 'new' });
    expect(placeOf('/new/be98')).toEqual({ kind: 'new', project: 'be98' });
    expect(placeOf('/')).toEqual({ kind: 'list' });
    expect(placeOf('/be98')).toEqual({ kind: 'list' });
    expect(placeOf('/a/b/c')).toEqual({ kind: 'list' });
  });

  it('writes paths that read back', () => {
    for (const p of ['/', '/new', '/new/be98', '/be98/wifi-drops']) expect(pathOf(placeOf(p))).toBe(p);
    expect(pathOf({ kind: 'conversation', project: 'be98', name: 'a b' })).toBe('/be98/a%20b');
    expect(placeOf('/be98/a%20b')).toEqual({ kind: 'conversation', project: 'be98', name: 'a b' });
  });

  it('sends links from before paths where they meant', () => {
    expect(fromHash('#c/be98/wifi-drops')).toBe('/be98/wifi-drops');
    expect(fromHash('#new')).toBe('/new');
    expect(fromHash('#new/be98')).toBe('/new/be98');
    expect(fromHash('#c/be98')).toBe('/');
    expect(fromHash('')).toBeNull();
  });
});
