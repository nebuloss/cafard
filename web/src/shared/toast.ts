/** Toaster shows one short line at the bottom of the screen, then hides it. */
export class Toaster {
  private static el: HTMLElement | null = null;
  private static timer: ReturnType<typeof setTimeout> | undefined;

  static show(message: string): void {
    if (!Toaster.el) {
      Toaster.el = document.createElement('div');
      Toaster.el.className = 'toast';
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
