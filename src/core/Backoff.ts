export interface BackoffOptions {
  /** First delay, before jitter. */
  readonly baseMs: number;
  /** No delay exceeds this. */
  readonly maxMs: number;
  /** Returns [0, 1). Injected for tests. */
  readonly random?: () => number;
}

/**
 * Exponential backoff with jitter, for SSE reconnects and upload retries.
 * Each delay is drawn from the upper half of the current step, so clients
 * that failed together do not retry together, and none retries at once.
 */
export class Backoff {
  private attempt = 0;
  private readonly random: () => number;

  constructor(private readonly options: BackoffOptions) {
    if (options.baseMs <= 0 || options.maxMs < options.baseMs) {
      throw new RangeError('Backoff needs 0 < baseMs <= maxMs');
    }
    this.random = options.random ?? Math.random;
  }

  get attempts(): number {
    return this.attempt;
  }

  /** The delay before the next try, in ms. */
  next(): number {
    const step = Math.min(this.options.maxMs, this.options.baseMs * 2 ** this.attempt);
    this.attempt++;
    return Math.round(step / 2 + this.random() * (step / 2));
  }

  /** Call after a success. */
  reset(): void {
    this.attempt = 0;
  }
}
