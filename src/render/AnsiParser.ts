/** A run of text and the ANSI classes that apply to it. */
export interface AnsiSegment {
  readonly class?: string;
  readonly text: string;
}

interface Sgr {
  fg: number;
  bg: number;
  bold: boolean;
  dim: boolean;
  italic: boolean;
  underline: boolean;
}

const reset = (): Sgr => ({ fg: -1, bg: -1, bold: false, dim: false, italic: false, underline: false });

function classOf(s: Sgr): string {
  const parts: string[] = [];
  if (s.fg >= 0) parts.push(`ansi-fg-${s.fg}`);
  if (s.bg >= 0) parts.push(`ansi-bg-${s.bg}`);
  if (s.bold) parts.push('ansi-bold');
  if (s.dim) parts.push('ansi-dim');
  if (s.italic) parts.push('ansi-italic');
  if (s.underline) parts.push('ansi-underline');
  return parts.join(' ');
}

function apply(s: Sgr, raw: readonly number[]): void {
  const params = raw.length === 0 ? [0] : raw;
  for (let i = 0; i < params.length; i++) {
    const p = params[i] ?? -1;
    if (p === 0) Object.assign(s, reset());
    else if (p === 1) s.bold = true;
    else if (p === 2) s.dim = true;
    else if (p === 3) s.italic = true;
    else if (p === 4) s.underline = true;
    else if (p === 22) s.bold = s.dim = false;
    else if (p === 23) s.italic = false;
    else if (p === 24) s.underline = false;
    else if (p >= 30 && p <= 37) s.fg = p - 30;
    else if (p === 39) s.fg = -1;
    else if (p >= 40 && p <= 47) s.bg = p - 40;
    else if (p === 49) s.bg = -1;
    else if (p >= 90 && p <= 97) s.fg = p - 90 + 8;
    else if (p >= 100 && p <= 107) s.bg = p - 100 + 8;
    else if (p === 38 || p === 48) {
      // 38;5;n keeps n when it is one of the 16 palette colours; 256-colour
      // and truecolour values are dropped, never passed on.
      if (i + 2 < params.length && params[i + 1] === 5) {
        const n = params[i + 2] ?? -1;
        if (n >= 0 && n < 16) {
          if (p === 38) s.fg = n;
          else s.bg = n;
        }
        i += 2;
      } else if (i + 1 < params.length && params[i + 1] === 2) {
        i += 4;
      }
    }
  }
}

function parseParams(s: string): number[] {
  return s
    .split(/[;:]/)
    .filter((f) => f !== '')
    .map((f) => {
      if (!/^[+-]?\d+$/.test(f)) return -1;
      const n = Number(f);
      return n > 255 ? -1 : n;
    });
}

const ESC = 0x1b;

/**
 * Parses SGR colour sequences into segments and drops every other escape
 * sequence and control character but tab and newline. Pure, and the same
 * algorithm as internal/web/view.ANSI; both are checked against
 * internal/web/testdata/ansi/vectors.json. Classes come from a fixed list,
 * so nothing of the input can reach an attribute.
 */
export function parseAnsi(text: string): AnsiSegment[] {
  const out: { class?: string; text: string }[] = [];
  const cur = reset();
  let cls = '';
  let buf = '';
  const flush = (): void => {
    if (buf === '') return;
    const last = out[out.length - 1];
    if (last !== undefined && (last.class ?? '') === cls) {
      last.text += buf;
    } else {
      out.push(cls === '' ? { text: buf } : { class: cls, text: buf });
    }
    buf = '';
  };
  let i = 0;
  while (i < text.length) {
    const c = text.charCodeAt(i);
    if (c === ESC) {
      flush();
      i = escape(text, i, cur);
      cls = classOf(cur);
      continue;
    }
    if ((c < 0x20 && c !== 0x09 && c !== 0x0a) || c === 0x7f || (c >= 0x80 && c < 0xa0)) {
      i++;
      continue;
    }
    buf += text[i];
    i++;
  }
  flush();
  return out;
}

function escape(text: string, i: number, cur: Sgr): number {
  if (i + 1 >= text.length) return i + 1;
  const kind = text[i + 1];
  if (kind === '[') {
    let j = i + 2;
    while (j < text.length && (text.charCodeAt(j) < 0x40 || text.charCodeAt(j) > 0x7e)) j++;
    if (j >= text.length) return j;
    if (text[j] === 'm') apply(cur, parseParams(text.slice(i + 2, j)));
    return j + 1;
  }
  if (kind === ']' || kind === 'P' || kind === '_' || kind === '^' || kind === 'X') {
    // String sequences end with BEL or ST (ESC \).
    for (let j = i + 2; j < text.length; j++) {
      if (text.charCodeAt(j) === 0x07) return j + 1;
      if (text.charCodeAt(j) === ESC && text[j + 1] === '\\') return j + 2;
    }
    return text.length;
  }
  if (kind === '(' || kind === ')' || kind === '*' || kind === '+') {
    // Character set designation: ESC ( B.
    return Math.min(i + 3, text.length);
  }
  // A two-character sequence: skip the whole character after ESC.
  const next = text.codePointAt(i + 1) ?? 0;
  return i + 1 + (next > 0xffff ? 2 : 1);
}
