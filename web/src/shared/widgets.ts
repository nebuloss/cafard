import { Bytes } from './format';
import { Html, html } from './html';
import { LANGS, i18n, n, t, type Lang } from './i18n';
import { Theme, type ThemeName } from './theme';
import { Icon } from './icons';

/** Who something is, as far as drawing it goes. */
export interface Who {
  name: string;
  kind: string;
  display_name?: string;
}

const AV = 'relative grid shrink-0 place-items-center font-bold text-on-surface';
const AV_SIZE = { md: 'size-10 text-sm', sm: 'size-7 text-[10.5px]' } as const;
const AV_KIND: Record<string, string> = {
  person: 'rounded-full bg-(--color-accent-pink)',
  agent: 'rounded-md3-s bg-(--color-accent-teal) font-mono font-semibold',
  device: 'rounded-md3-s bg-(--color-accent-sand) font-mono font-semibold',
};

/**
 * The language, as agendouille has it at the top right: a small dropdown
 * showing the current flag and code, then the languages, each with its flag
 * and its name in itself. Choosing another reloads the page in it. A native
 * <details>, so it opens without script; a click elsewhere or Escape closes
 * it. The page that draws it maps the "lang" action to LangMenu.choose.
 *
 * A flag stands for a language beside its name, never alone: French is not
 * only France's, but the flag is what people scan for.
 */
export class LangMenu {
  private static readonly FLAGS: Record<Lang, string> = {
    fr: '<rect width="10" height="20" fill="#002654"/><rect x="10" width="10" height="20" fill="#fff"/><rect x="20" width="10" height="20" fill="#ce1126"/>',
    // The Union flag, simplified to read at 20 px: the saltires, then the cross.
    en: '<rect width="30" height="20" fill="#012169"/><path d="M0 0L30 20M30 0L0 20" stroke="#fff" stroke-width="4"/><path d="M0 0L30 20M30 0L0 20" stroke="#c8102e" stroke-width="1.6"/><path d="M15 0v20M0 10h30" stroke="#fff" stroke-width="6"/><path d="M15 0v20M0 10h30" stroke="#c8102e" stroke-width="3.4"/>',
  };

  /** A language's flag, 3:2, rounded by the element's own radius. */
  static flag(l: Lang, height = 14): Html {
    return Html.raw(`<svg class="shrink-0 overflow-hidden rounded-[3px] ring-1 ring-on-surface/20" viewBox="0 0 30 20" width="${Math.round(height * 1.5)}" height="${height}" aria-hidden="true">${LangMenu.FLAGS[l]}</svg>`);
  }

  /** compact shows the flag alone, for a narrow header (the chat's list); the menu still names each language. */
  static view(compact = false): Html {
    const cur = i18n.lang;
    const name = LANGS.find((l) => l.id === cur)?.name ?? cur;
    const item = (l: (typeof LANGS)[number]) => {
      const on = l.id === cur;
      return html`<button type="button" class="state-layer flex items-center gap-3 rounded-md3-m px-3 py-2 text-left text-sm whitespace-nowrap ${on ? 'font-semibold' : ''}" role="menuitemradio" aria-checked="${String(on)}" lang="${l.id}" data-act="lang" data-v="${l.id}">${LangMenu.flag(l.id, 15)}<span class="flex-1">${l.name}</span>${on ? Icon.of('check') : ''}</button>`;
    };
    return html`<details class="lang-menu relative shrink-0" data-menu>
      <summary class="state-layer inline-flex h-10 cursor-pointer list-none items-center gap-1.5 rounded-md3-full border border-outline-variant px-2.5 text-[13px] font-semibold text-on-surface-variant [&::-webkit-details-marker]:hidden [&_.ic]:size-4" title="${t('Language')}" aria-label="${t('Language')}: ${name}">${LangMenu.flag(cur, 13)}${compact ? '' : html`<span class="uppercase">${cur}</span>${Icon.of('chevDown')}`}</summary>
      <div class="absolute right-0 z-50 mt-1.5 flex min-w-44 flex-col rounded-md3-l bg-surface-high p-1.5 text-on-surface elevation-2 [&_.ic]:size-4" role="menu">${LANGS.map(item)}</div></details>`;
  }

  static choose(el: HTMLElement): void {
    const l = LANGS.find((x) => x.id === el.dataset['v']);
    el.closest('details')?.removeAttribute('open');
    if (l && l.id !== i18n.lang) i18n.choose(l.id);
  }
}

// An open menu closes on a click outside it, or on Escape.
if (typeof document !== 'undefined') {
  document.addEventListener('click', (e) => {
    for (const d of Array.from(document.querySelectorAll<HTMLDetailsElement>('details[data-menu][open]'))) {
      if (!d.contains(e.target as Node)) d.open = false;
    }
  });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    for (const d of Array.from(document.querySelectorAll<HTMLDetailsElement>('details[data-menu][open]'))) {
      d.open = false;
      d.querySelector('summary')?.focus();
    }
  });
}

/**
 * The theme, as claudication has it: three states rather than a switch,
 * light, the system's and dark, as a segmented control. "The system's" is a
 * standing instruction to keep following the machine, which a two-way
 * toggle cannot express. The page maps the "theme" action to
 * ThemeSwitch.choose.
 */
export class ThemeSwitch {
  private static options(): { id: ThemeName; label: string; icon: Html }[] {
    return [
      { id: 'light', label: t('Light'), icon: Icon.of('sun') },
      { id: 'auto', label: t('Follow the system'), icon: Icon.of('monitor') },
      { id: 'dark', label: t('Dark'), icon: Icon.of('moon') },
    ];
  }

  /** md is the header's 40px, beside its other controls; sm the sign-in card's. */
  static view(size: 'sm' | 'md' = 'md'): Html {
    const cur = Theme.current();
    const box = size === 'md' ? 'h-10 min-w-10 px-3' : 'h-9 min-w-9 px-2';
    const one = (o: { id: ThemeName; label: string; icon: Html }, i: number) =>
      html`<button type="button" class="state-layer grid ${box} place-items-center [&_.ic]:size-4 ${i > 0 ? 'border-l border-outline' : ''} ${o.id === cur ? 'bg-secondary-container text-on-secondary-container' : 'text-on-surface-variant'}" role="radio" aria-checked="${String(o.id === cur)}" data-act="theme" data-v="${o.id}" title="${o.label}" aria-label="${o.label}">${o.icon}</button>`;
    return html`<div class="theme-switch inline-flex shrink-0 overflow-hidden rounded-md3-full border border-outline" role="radiogroup" aria-label="${t('Colour theme')}">${ThemeSwitch.options().map(one)}</div>`;
  }

  static choose(el: HTMLElement): void {
    const v = el.dataset['v'];
    if (v !== 'light' && v !== 'dark' && v !== 'auto') return;
    Theme.set(v);
    // Every switch on the page shows the choice, not only the one clicked.
    for (const b of Array.from(document.querySelectorAll<HTMLElement>('.theme-switch [data-act="theme"]'))) {
      const on = b.dataset['v'] === v;
      b.setAttribute('aria-checked', String(on));
      b.classList.toggle('bg-secondary-container', on);
      b.classList.toggle('text-on-secondary-container', on);
      b.classList.toggle('text-on-surface-variant', !on);
    }
  }
}

/**
 * The header's word for a stream that is down, as claudication's
 * freshness says "offline": a page that cannot hear changes says so, and
 * reads every half minute meanwhile.
 */
export class OfflineChip {
  static view(): Html {
    return html`<span class="inline-flex h-7 shrink-0 items-center gap-1.5 rounded-full bg-warning-container px-2.5 text-[12px] font-semibold text-on-warning-container [&_.ic]:size-3.5" role="status" title="${t('Changes show within half a minute until the connection is back.')}">${Icon.of('wifiOff')}${t('Offline')}</span>`;
  }

  /**
   * Shows the chip in every .live-slot while stream is down, after a grace
   * period: a stream reopening after a deploy is not worth a flash.
   */
  static bind(stream: { live: boolean; watch(fn: () => void): () => void }): void {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const draw = () => {
      for (const slot of Array.from(document.querySelectorAll<HTMLElement>('.live-slot'))) slot.innerHTML = stream.live ? '' : OfflineChip.view().value;
    };
    stream.watch(() => {
      clearTimeout(timer);
      if (stream.live) draw();
      else timer = setTimeout(draw, 4000);
    });
  }
}

/**
 * The link to cafard's source, as claudication's header has it: GitHub's
 * mark in a round outlined button, opened in a new tab.
 */
export class SourceLink {
  static readonly REPO = 'https://github.com/nebuloss/cafard';

  private static readonly MARK =
    '<svg viewBox="0 0 24 24" class="size-5 fill-current" aria-hidden="true"><path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12"/></svg>';

  static view(): Html {
    return html`<a class="state-layer grid size-10 shrink-0 place-items-center rounded-md3-full border border-outline text-on-surface-variant" href="${SourceLink.REPO}" target="_blank" rel="noopener noreferrer" title="${t('Source on GitHub')}" aria-label="${t('Source on GitHub')}">${Html.raw(SourceLink.MARK)}</a>`;
  }
}

/** What a kind of member is called on screen: its pill, its line under a name. */
export class KindName {
  static of(kind: string): string {
    if (kind === 'person') return t('person');
    if (kind === 'agent') return t('agent');
    if (kind === 'device') return t('device');
    return kind;
  }
}

/** Avatars: round for people, square for agents and devices, so form says who is a machine. */
export class Avatar {
  static of(m: Who, size: 'md' | 'sm' = 'md'): Html {
    const letters = m.kind === 'person' ? (m.display_name || m.name).charAt(0).toUpperCase() : m.name.slice(0, 2).toUpperCase();
    return html`<span class="${AV} ${AV_SIZE[size]} ${AV_KIND[m.kind] ?? AV_KIND['person']}" title="${m.name}">${letters}</span>`;
  }

  static project(name: string, size: 'md' | 'sm' = 'md'): Html {
    return html`<span class="${AV} ${AV_SIZE[size]} rounded-md3-s bg-(--color-accent-blue)">${name.slice(0, 2).toUpperCase()}</span>`;
  }
}

/** A switch, on or off, that runs an action (MD3's switch). */
export class Switch {
  static of(on: boolean, act: string, value: string, label: string): Html {
    return html`<button class="relative h-8 w-[52px] shrink-0 rounded-full border-2 border-outline bg-surface-highest transition-colors after:absolute after:top-1/2 after:left-1.5 after:size-4 after:-translate-y-1/2 after:rounded-full after:bg-outline after:transition-all aria-checked:border-primary aria-checked:bg-primary aria-checked:after:left-6 aria-checked:after:size-[22px] aria-checked:after:bg-on-primary" role="switch" aria-checked="${String(on)}" aria-label="${label}" data-act="${act}" data-v="${value}"></button>`;
  }
}

/** How full a project is, against its quota. */
export class Meter {
  static of(p: { bytes: number; quota_bytes: number; retention_days: number }): Html {
    const pct = p.quota_bytes ? (p.bytes / p.quota_bytes) * 100 : 0;
    const warn = pct >= 80;
    return html`<div class="min-w-0"><div class="flex justify-between gap-2 text-[12.5px] tabular-nums"><span class="font-semibold">${Bytes.format(p.bytes)}</span><span class="text-on-surface-variant">${n(p.retention_days, 'of {total} · {n} day', 'of {total} · {n} days', { total: Bytes.format(p.quota_bytes) })}</span></div>
      <div class="relative mt-1 h-2 overflow-hidden rounded bg-surface-highest" role="img" aria-label="${t('{pct}% of quota used', { pct: Math.round(pct) })}"><i class="absolute inset-y-0 left-0 rounded ${warn ? 'bg-warning' : 'bg-secondary'}" data-w="${pct.toFixed(2)}"></i></div>
      ${warn ? html`<div class="mt-1 flex items-center gap-1 text-xs font-semibold text-warning [&_.ic]:size-3.5">${Icon.of('warn')}${t('{pct}% of quota: uploads stop at 100%', { pct: Math.round(pct) })}</div>` : ''}</div>`;
  }
}
