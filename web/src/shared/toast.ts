import { t } from './i18n';

/**
 * UpdateBar says a new version of the UI was deployed since this page
 * loaded, until the reader reloads, as claudication's does: a page left
 * open for days would otherwise run old code against a new server.
 */
export class UpdateBar {
  private static shown = false;

  static show(): void {
    if (UpdateBar.shown) return;
    UpdateBar.shown = true;
    const bar = document.createElement('div');
    bar.className = 'fixed bottom-4 left-1/2 z-50 flex max-w-[calc(100%-32px)] -translate-x-1/2 items-center gap-3 rounded-md3-m bg-on-surface py-2 pr-2 pl-4 text-sm text-surface elevation-2';
    bar.setAttribute('role', 'status');
    const text = document.createElement('span');
    text.textContent = t('A new version of cafard is ready.');
    const reload = document.createElement('button');
    reload.type = 'button';
    reload.className = 'state-layer h-8 rounded-md3-full px-3 font-semibold text-primary-container';
    reload.textContent = t('Reload');
    reload.addEventListener('click', () => location.reload());
    bar.append(text, reload);
    document.body.append(bar);
  }
}

/** Toaster shows one short line at the bottom of the screen, then hides it. */
export class Toaster {
  private static el: HTMLElement | null = null;
  private static timer: ReturnType<typeof setTimeout> | undefined;

  static show(message: string): void {
    if (!Toaster.el) {
      Toaster.el = document.createElement('div');
      Toaster.el.className =
        'fixed bottom-6 left-1/2 z-50 max-w-[calc(100%-32px)] -translate-x-1/2 rounded-md3-s bg-on-surface px-4 py-3 text-sm text-surface elevation-2';
      Toaster.el.setAttribute('role', 'status');
      document.body.append(Toaster.el);
    }
    const el = Toaster.el;
    el.textContent = message;
    el.hidden = false;
    clearTimeout(Toaster.timer);
    Toaster.timer = setTimeout(() => {
      el.hidden = true;
    }, 3200);
  }
}
