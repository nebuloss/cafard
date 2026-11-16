import type { Clock } from '@/core/Clock.ts';
import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom } from '@/core/Dom.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell } from '@/pages/Shell.ts';

/** /pair: the 5-minute countdown under each pending QR code. */
export class PairPage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const shell = mountShell(this.doc, this.services, bag, '/api/events?scope=status');
    for (const panel of Dom.findAll('[data-component="PairPanel"]', this.doc)) {
      bag.add(countdown(panel, this.services.clock));
    }
    bag.add(shell.feed.start());
    return bag;
  }
}

/** Counts down to data-expires, then greys the panel out. */
export function countdown(panel: HTMLElement, clock: Clock): Disposable {
  const bag = new DisposableBag();
  const expires = Date.parse(panel.dataset['expires'] ?? '');
  const slot = Dom.find('[data-slot="countdown"]', panel);
  if (Number.isNaN(expires) || slot === null) return bag;
  let timer: Disposable | null = null;
  const tick = (): void => {
    const left = Math.max(0, Math.ceil((expires - clock.now()) / 1000));
    if (left === 0) {
      panel.dataset['expired'] = '';
      Dom.setText(slot, 'Expired; make a new code.');
      return;
    }
    Dom.setText(slot, `Expires in ${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}.`);
    timer = clock.setTimeout(tick, 1000);
  };
  tick();
  bag.defer(() => timer?.dispose());
  return bag;
}
