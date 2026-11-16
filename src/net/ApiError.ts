/**
 * An error the server described (CONTRACTS.md 3.2), or a failure to reach
 * it. message is the server's own sentence and is shown as is: the server
 * owns the wording.
 */
export class ApiError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly status: number,
    readonly retryAfterS?: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}
