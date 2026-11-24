/**
 * The UIs' languages. A string on screen is written in English in the
 * source, passed through t() (or n() when it counts something), and looked
 * up in the language's catalog under that English text; a string missing
 * from a catalog shows in English rather than not at all, and a test
 * (test/i18n.test.ts) fails the build when one is missing.
 *
 * Placeholders are {name}, filled from vars and escaped by the html tag like
 * any other value, so a catalog can move them where its grammar needs them.
 *
 * The language is the browser's own, until the person chooses one (in the
 * chat's profile, in the admin UI's settings). The choice is kept in a
 * cookie, cafard_lang, rather than only in storage: the pages the server
 * writes (a join link opened in a browser) and the refusals the APIs send
 * back read it there (internal/i18n).
 */

import { fr } from './locales/fr';

export type Lang = 'en' | 'fr';

/** The languages there are, each named in itself, as a picker lists them. */
export const LANGS: readonly { id: Lang; name: string }[] = [
  { id: 'en', name: 'English' },
  { id: 'fr', name: 'Français' },
];

/** English text → its translation. */
export type Catalog = Readonly<Record<string, string>>;

const CATALOGS: Record<Lang, Catalog> = { en: {}, fr };

const COOKIE = 'cafard_lang';
const STORE = 'cafard.lang';

type Vars = Readonly<Record<string, string | number>>;

export class I18n {
  private current: Lang;
  private plurals: Intl.PluralRules;

  constructor(lang?: Lang) {
    this.current = lang ?? I18n.detect();
    this.plurals = new Intl.PluralRules(this.current);
  }

  get lang(): Lang {
    return this.current;
  }

  /** The locale Intl formats dates and numbers in. */
  get locale(): string {
    return this.current === 'fr' ? 'fr-FR' : 'en-GB';
  }

  /** text in the current language, its {placeholders} filled from vars. */
  t(text: string, vars?: Vars): string {
    return I18n.fill(CATALOGS[this.current][text] ?? text, vars);
  }

  /**
   * A count with its words: one when count is one in English, other
   * otherwise, each looked up as t() does. {n} is the count. French puts
   * 0 with "one" as well, which Intl's plural rules say.
   */
  n(count: number, one: string, other: string, vars?: Vars): string {
    const form = this.current === 'en' ? (count === 1 ? 'one' : 'other') : this.plurals.select(count);
    return this.t(form === 'one' ? one : other, { n: count, ...vars });
  }

  /** Chooses lang for this browser, and reloads: every page is drawn again in it. */
  choose(lang: Lang): void {
    try {
      localStorage.setItem(STORE, lang);
    } catch {
      /* storage refused: the cookie is enough */
    }
    document.cookie = `${COOKIE}=${lang}; Path=/; Max-Age=${400 * 24 * 3600}; SameSite=Lax${location.protocol === 'https:' ? '; Secure' : ''}`;
    location.reload();
  }

  /** Marks the document with the language, for screen readers and hyphenation. */
  apply(doc: Document = document): void {
    doc.documentElement.lang = this.current;
  }

  /** The language chosen here before, else the browser's first one cafard has, else English. */
  static detect(): Lang {
    const known = (s: string | null | undefined): Lang | null => {
      const l = (s ?? '').toLowerCase().slice(0, 2);
      return LANGS.some((x) => x.id === l) ? (l as Lang) : null;
    };
    if (typeof document === 'undefined') return 'en';
    let stored: string | null = null;
    try {
      stored = localStorage.getItem(STORE);
    } catch {
      /* no storage */
    }
    const cookie = document.cookie.match(/(?:^|;\s*)cafard_lang=([a-z]+)/)?.[1];
    for (const c of [stored, cookie, ...(navigator.languages ?? [navigator.language])]) {
      const l = known(c);
      if (l) return l;
    }
    return 'en';
  }

  static fill(s: string, vars?: Vars): string {
    if (!vars) return s;
    return s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
  }
}

export const i18n = new I18n();

/** i18n.t, for short. */
export const t = (text: string, vars?: Vars): string => i18n.t(text, vars);

/** i18n.n, for short. */
export const n = (count: number, one: string, other: string, vars?: Vars): string => i18n.n(count, one, other, vars);
