import { Html } from './html';

const PATHS = {
  people: '<circle cx="9" cy="8" r="3.2"/><path d="M3 19c.6-3.3 3-5 6-5s5.4 1.7 6 5"/><rect x="15" y="6" width="6" height="6" rx="1.5"/><path d="M15 17h6"/>',
  folder: '<path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z"/>',
  list: '<path d="M8 6h12M8 12h12M8 18h12"/><circle cx="4" cy="6" r="1"/><circle cx="4" cy="12" r="1"/><circle cx="4" cy="18" r="1"/>',
  disk: '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6"/><path d="M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  close: '<path d="M6 6l12 12M18 6L6 18"/>',
  chev: '<path d="M9 5l7 7-7 7"/>',
  back: '<path d="M15 5l-7 7 7 7"/>',
  search: '<circle cx="11" cy="11" r="6"/><path d="M20 20l-4.5-4.5"/>',
  warn: '<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17v.5"/>',
  check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8v.5"/>',
  lock: '<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 018 0v3"/>',
  home: '<path d="M4 11l8-7 8 7"/><path d="M6 9.5V20h4.5v-5h3v5H18V9.5"/>',
  key: '<circle cx="8" cy="15" r="4"/><path d="M11 12l8-8M16 7l2.5 2.5M14 9l2 2"/>',
  person: '<circle cx="12" cy="8" r="3.5"/><path d="M5 20c.8-3.8 3.6-6 7-6s6.2 2.2 7 6"/>',
} as const;

export type IconName = keyof typeof PATHS;

/** Line icons, drawn in the current colour. */
export class Icon {
  static of(name: IconName): Html {
    return Html.raw(`<svg class="ic" viewBox="0 0 24 24" aria-hidden="true">${PATHS[name]}</svg>`);
  }

  /** cafard's mark. */
  static readonly logo = Html.raw(
    '<svg viewBox="0 0 32 32" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><ellipse cx="16" cy="19" rx="7" ry="9.5" fill="currentColor" fill-opacity=".18"/><path d="M16 10v18"/><path d="M13 9.5C11 6 8 4 5 4M19 9.5C21 6 24 4 27 4"/><path d="M9.5 16 5 14M9.5 21 4.5 22M22.5 16 27 14M22.5 21l5 1"/></svg>',
  );
}
