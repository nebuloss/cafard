import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { I18n } from '@shared/i18n';
import { fr } from '@shared/locales/fr';

/** Every .ts file under dir. */
function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) return f === 'locales' ? [] : sources(p);
    return p.endsWith('.ts') ? [p] : [];
  });
}

/** A string literal's text, from its quotes. */
const lit = String.raw`'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"`;
const unquote = (m: RegExpMatchArray, from: number): string => {
  const s = m[from] ?? m[from + 1] ?? '';
  return s.replace(/\\(.)/g, '$1');
};

/**
 * The English texts the source passes to t() and n(), literally: what a
 * catalog must have. A t() of a variable is not checked; keep those to
 * values that went through t() already.
 */
function used(): Map<string, string> {
  const out = new Map<string, string>();
  const tCall = new RegExp(String.raw`(?<![\w.])t\(\s*(?:${lit})`, 'g');
  const nCall = new RegExp(String.raw`(?<![\w.])n\(\s*[^,]+,\s*(?:${lit})\s*,\s*(?:${lit})`, 'g');
  for (const f of sources(join(__dirname, '..', 'src'))) {
    const src = readFileSync(f, 'utf8');
    for (const m of src.matchAll(tCall)) out.set(unquote(m, 1), f);
    for (const m of src.matchAll(nCall)) {
      out.set(unquote(m, 1), f);
      out.set(unquote(m, 3), f);
    }
  }
  return out;
}

const holes = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

describe('i18n', () => {
  it('has French for every text on screen', () => {
    const missing = [...used()].filter(([text]) => !(text in fr)).map(([text, f]) => `${f.split('/src/')[1]}: ${JSON.stringify(text)}`);
    expect(missing).toEqual([]);
  });

  it('keeps every placeholder in the French', () => {
    const wrong = Object.entries(fr).filter(([en, f]) => holes(en).join() !== holes(f).join()).map(([en]) => en);
    expect(wrong).toEqual([]);
  });

  it('has no French for text no longer on screen', () => {
    const u = used();
    expect(Object.keys(fr).filter((k) => !u.has(k))).toEqual([]);
  });

  it('fills placeholders and falls back to English', () => {
    const en = new I18n('en');
    const f = new I18n('fr');
    expect(en.t('{n} min ago', { n: 3 })).toBe('3 min ago');
    expect(f.t('{n} min ago', { n: 3 })).toBe('il y a 3 min');
    expect(f.t('not in any catalog {x}', { x: 1 })).toBe('not in any catalog 1');
    expect(en.n(1, '{n} day ago', '{n} days ago')).toBe('1 day ago');
    expect(en.n(0, '{n} day ago', '{n} days ago')).toBe('0 days ago');
    expect(f.n(0, '{n} day ago', '{n} days ago')).toBe('il y a 0 jour');
    expect(f.n(2, '{n} day ago', '{n} days ago')).toBe('il y a 2 jours');
  });
});
