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
