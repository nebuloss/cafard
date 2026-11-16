import { describe, expect, it } from 'vitest';
import { parseAnsi } from '@/render/AnsiParser.ts';
import { readTestdata } from '@/testing/pages.ts';

interface Vector {
  in: string;
  out: { class?: string; text: string }[];
}

describe('AnsiParser', () => {
  const vectors = JSON.parse(readTestdata('ansi/vectors.json')) as Vector[];

  it('agrees with the Go parser on every shared vector', () => {
    expect(vectors.length).toBeGreaterThan(10);
    for (const v of vectors) expect(parseAnsi(v.in), JSON.stringify(v.in)).toEqual(v.out);
  });

  it('never turns input into a class outside the fixed list', () => {
    const classes = new Set(parseAnsi('\x1b[38;5;3m\x1b[1;4;101mx\x1b[0m\x1b[999;38;5;77my').flatMap((s) => (s.class ?? '').split(' ')));
    for (const c of classes) expect(c === '' || /^ansi-(fg|bg)-\d{1,2}$|^ansi-(bold|dim|italic|underline)$/.test(c)).toBe(true);
  });
});
