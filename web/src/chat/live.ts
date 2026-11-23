import type { Changed } from './types';

/**
 * LiveFeed listens to /api/v1/events and passes on what changed. The
 * browser's EventSource reconnects by itself; after a reconnection the page
 * may have missed notices, so it is told to read everything again.
 */
export class LiveFeed {
  private source: EventSource | null = null;
  private connectedOnce = false;

  constructor(private readonly onChange: (c: Changed) => void) {}

  start(): void {
    if (this.source) return;
    const src = new EventSource('/api/v1/events', { withCredentials: true });
    src.addEventListener('ready', () => {
      if (this.connectedOnce) this.onChange({ topic: 'resync' });
      this.connectedOnce = true;
    });
    src.addEventListener('changed', (e) => {
      try {
        this.onChange(JSON.parse((e as MessageEvent<string>).data) as Changed);
      } catch {
        /* a malformed notice is dropped; the next one will do */
      }
    });
    this.source = src;
  }

  stop(): void {
    this.source?.close();
    this.source = null;
    this.connectedOnce = false;
  }
}
