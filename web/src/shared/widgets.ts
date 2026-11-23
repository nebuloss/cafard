import { Bytes } from './format';
import { Html, html } from './html';
import { Icon } from './icons';

/** Who something is, as far as drawing it goes. */
export interface Who {
  name: string;
  kind: string;
  display_name?: string;
}

/** Avatars: round for people, square for agents and devices, so form says who is a machine. */
export class Avatar {
  static of(m: Who, extra = ''): Html {
    const letters = m.kind === 'person' ? (m.display_name || m.name).charAt(0).toUpperCase() : m.name.slice(0, 2).toUpperCase();
    const cls = m.kind === 'person' ? '' : m.kind;
    return html`<span class="av ${cls} ${extra}" title="${m.name}">${letters}</span>`;
  }

  static project(name: string, extra = ''): Html {
    return html`<span class="av proj ${extra}">${name.slice(0, 2).toUpperCase()}</span>`;
  }
}

/** A switch, on or off, that runs an action. */
export class Switch {
  static of(on: boolean, act: string, value: string, label: string): Html {
    return html`<button class="switch" role="switch" aria-checked="${String(on)}" aria-label="${label}" data-act="${act}" data-v="${value}"></button>`;
  }
}

/** How full a project is, against its quota. */
export class Meter {
  static of(p: { bytes: number; quota_bytes: number; retention_days: number }): Html {
    const pct = p.quota_bytes ? (p.bytes / p.quota_bytes) * 100 : 0;
    const warn = pct >= 80;
    return html`<div class="meter ${warn ? 'warn' : ''}"><div class="mt"><span>${Bytes.format(p.bytes)}</span><span>of ${Bytes.format(p.quota_bytes)} · ${p.retention_days}-day retention</span></div>
      <div class="bar" role="img" aria-label="${Math.round(pct)}% of quota used"><i data-w="${pct.toFixed(2)}"></i></div>
      ${warn ? html`<div class="note">${Icon.of('warn')}${Math.round(pct)}% of quota: uploads stop at 100%</div>` : ''}</div>`;
  }
}
