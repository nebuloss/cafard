import { Dom, h } from '@/core/Dom.ts';
import type { Chat, Stream } from '@/model/types.ts';
import { Component } from '@/components/Component.ts';

/**
 * The chat header (PLAN 9.6.3): bound stream chips that follow the chat's
 * bindings and the sources' status, and the settings button. The
 * Chat/Logs toggle is a pair of links laid out by CSS.
 */
export class ChannelHeader extends Component {
  constructor(
    root: HTMLElement,
    private readonly openSettings: () => void,
  ) {
    super(root);
  }

  protected bind(): void {
    const settings = Dom.find('[data-action="settings"]', this.root);
    if (settings !== null) this.bag.listen(settings, 'click', () => this.openSettings());
  }

  /** The chat's streams changed (a chat event). */
  update(chat: Chat): void {
    let chips = Dom.find('[data-slot="streams"]', this.root);
    if (chips === null) {
      chips = h('div', { class: 'ch-streams', 'data-slot': 'streams' });
      this.root.append(chips);
    }
    chips.replaceChildren(
      ...chat.streams.map((s) =>
        h(
          'a',
          { class: 'chip stream-chip mono', href: `/live?${new URLSearchParams({ streams: s.id }).toString()}`, 'data-stream': s.id, 'data-status': s.status },
          h('span', { class: 'stream-dot', 'aria-hidden': 'true' }),
          s.id,
        ),
      ),
    );
  }

  status(s: Stream): void {
    for (const chip of Dom.findAll('[data-stream]', this.root)) {
      if (chip.dataset['stream'] === s.id) chip.dataset['status'] = s.status;
    }
  }

  /** The dot on the Logs toggle: an error arrived while the chat is shown. */
  errorDot(on: boolean): void {
    const dot = Dom.find('[data-slot="error-dot"]', this.root);
    if (dot !== null) dot.hidden = !on;
  }
}
