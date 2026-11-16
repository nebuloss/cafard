import { Dom } from '@/core/Dom.ts';
import type { Observable } from '@/core/Observable.ts';
import type { FeedState } from '@/net/LiveFeed.ts';
import { Component } from '@/components/Component.ts';

const LABELS: Readonly<Record<FeedState['state'], string>> = {
  connecting: 'connecting',
  live: 'live',
  retrying: 'reconnecting',
  closed: 'offline',
};

/** The top bar badge: live, reconnecting or offline (PLAN 9.6.2). */
export class ConnectionBadge extends Component {
  constructor(
    root: HTMLElement,
    private readonly state: Observable<FeedState>,
  ) {
    super(root);
  }

  protected bind(): void {
    const label = Dom.find('[data-slot="label"]', this.root);
    this.bag.add(
      this.state.subscribe(
        (s) => {
          this.root.dataset['state'] = s.state;
          Dom.setText(label, LABELS[s.state]);
        },
        { immediate: true },
      ),
    );
  }
}
