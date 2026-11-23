// Markup is built from tagged templates that escape what they are given.
//
// html`<b>${name}</b>` escapes name; html`${other}` keeps other as it is
// when it is already Html, which is how components nest. So a value that
// came from the server (a member's name, a log line, a file name) cannot
// become markup by being forgotten: escaping is the default, and leaving
// something unescaped takes Html.raw, which says so.

/** Markup that is safe to put in the page as it is. */
export class Html {
  constructor(readonly value: string) {}

  /** Trusted markup, written in this code base: icons, fixed strings. */
  static raw(value: string): Html {
    return new Html(value);
  }

  static readonly empty = new Html('');

  /** Joins a list of Html, as `items.map(...)` inside a template does. */
  static join(parts: Html[], sep = ''): Html {
    return new Html(parts.map((p) => p.value).join(sep));
  }

  toString(): string {
    return this.value;
  }
}

const ENTITIES: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };

/** Escapes text for use in an element or a quoted attribute. */
export function escape(text: unknown): string {
  return String(text ?? '').replace(/[&<>"']/g, (c) => ENTITIES[c] ?? c);
}

type Part = Html | string | number | boolean | null | undefined | Part[];

function render(part: Part): string {
  if (part instanceof Html) return part.value;
  if (Array.isArray(part)) return part.map(render).join('');
  if (part === null || part === undefined || part === false) return '';
  return escape(part);
}

/** The template tag. */
export function html(strings: TemplateStringsArray, ...values: Part[]): Html {
  let out = strings[0] ?? '';
  values.forEach((v, i) => {
    out += render(v) + (strings[i + 1] ?? '');
  });
  return new Html(out);
}
