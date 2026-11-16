import { ApiError } from '@/net/ApiError.ts';

export { ApiError };

export type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

export interface RequestOptions<T> {
  readonly query?: Readonly<Record<string, string | number | undefined>>;
  readonly body?: unknown;
  /** Validates the JSON answer. Omitted for 204 answers. */
  readonly parse?: (raw: unknown) => T;
}

/** The edge to the server. Pages never call fetch; CafardApi does, through this. */
export interface HttpClient {
  request<T>(method: Method, path: string, options?: RequestOptions<T>): Promise<T>;
}

export type FetchFn = (input: string, init: RequestInit) => Promise<Response>;

/**
 * fetch, same-origin only. Every request carries X-Cafard: 1, which tells
 * form routes to answer JSON. It is not a security control: CSRF is refused
 * on Sec-Fetch-Site, which the browser sets.
 */
export class FetchHttpClient implements HttpClient {
  constructor(private readonly fetchFn: FetchFn = (input, init) => globalThis.fetch(input, init)) {}

  async request<T>(method: Method, path: string, options: RequestOptions<T> = {}): Promise<T> {
    if (!path.startsWith('/') || path.startsWith('//')) throw new Error(`not a same-origin path: ${path}`);
    const url = path + queryString(options.query);
    const headers: Record<string, string> = { 'X-Cafard': '1', Accept: 'application/json' };
    const init: RequestInit = { method, headers, credentials: 'same-origin', redirect: 'error' };
    if (options.body !== undefined) {
      headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(options.body);
    }
    let response: Response;
    try {
      response = await this.fetchFn(url, init);
    } catch {
      throw ApiError.offline();
    }
    if (response.status === 204) return undefined as T;
    let raw: unknown = null;
    try {
      raw = await response.json();
    } catch {
      raw = null;
    }
    if (!response.ok) throw ApiError.fromResponse(raw, response.status);
    if (options.parse === undefined) return raw as T;
    return options.parse(raw);
  }
}

function queryString(query: RequestOptions<unknown>['query']): string {
  if (query === undefined) return '';
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  const s = params.toString();
  return s === '' ? '' : `?${s}`;
}
