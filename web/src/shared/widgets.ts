import { Bytes } from './format';
import { Html, html } from './html';
import { n, t } from './i18n';
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
