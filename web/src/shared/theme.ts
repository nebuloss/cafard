/**
 * The reader's theme: follow the system, or always light, or always dark,
 * as agendouille does. Kept in a cookie (cafard_theme) so the server puts
 * data-theme on <html> before the first paint (httpx.SPA, the join pages);
 * applied here at once too. design/theme.css reads data-theme over the
 * system's preference.
 *
 * The pages carry Dark Reader's lock (<meta name="darkreader-lock">): this
 * is their own dark mode, and Dark Reader inverting it made it unreadable.
 * Someone who wants dark whatever their system says chooses it here.
 */
export type ThemeName = 'auto' | 'light' | 'dark';

const COOKIE = 'cafard_theme';

export class Theme {
  static current(doc: Document = document): ThemeName {
    const v = doc.documentElement.dataset['theme'];
    return v === 'light' || v === 'dark' ? v : 'auto';
  }

  static set(theme: ThemeName, doc: Document = document): void {
    const root = doc.documentElement;
    const secure = doc.location?.protocol === 'https:' ? '; Secure' : '';
    if (theme === 'auto') {
      delete root.dataset['theme'];
      doc.cookie = `${COOKIE}=; Path=/; Max-Age=0; SameSite=Lax${secure}`;
    } else {
      root.dataset['theme'] = theme;
      doc.cookie = `${COOKIE}=${theme}; Path=/; Max-Age=${400 * 24 * 3600}; SameSite=Lax${secure}`;
    }
    Theme.paintBar(doc);
  }

  /**
   * The <meta theme-color> pair answers the system's preference only, so an
   * explicit choice drives the browser's bar from the palette itself, once
   * the change has landed; following the system again, the pair answers.
   */
  static paintBar(doc: Document = document): void {
    const metas = Array.from(doc.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]'));
    requestAnimationFrame(() => {
      const explicit = Theme.current(doc) !== 'auto';
      const bg = getComputedStyle(doc.documentElement).getPropertyValue('--color-surface').trim();
      for (const m of metas) {
        m.dataset['system'] ??= m.content;
        m.content = explicit && bg ? bg : (m.dataset['system'] ?? m.content);
      }
    });
  }
}
