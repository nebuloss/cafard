import { parseError } from '@/model/parse.ts';

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

  static offline(): ApiError {
    return new ApiError('offline', 'The server cannot be reached.', 0);
  }

  /** The error body of a failed answer, or a generic one when it has none. */
  static fromResponse(raw: unknown, status: number): ApiError {
    try {
      const e = parseError(raw);
      return new ApiError(e.code, e.message, status, e.retry_after_s);
    } catch {
      return new ApiError('http_error', `The server answered ${status}.`, status);
    }
  }
}
