import { t } from './i18n';

/** A refusal from cafard: a code to act on and a sentence to show. */
export class Refused extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = 'Refused';
  }
}

/**
 * ApiClient talks JSON to one of cafard's APIs, with the page's sign-in
 * cookie. Every refusal comes back as a Refused whose message says what to
 * do instead, which is why most callers do nothing with an error but show
 * it. A 401 also tells onSignedOut, so the page can ask to sign in again.
 */
export class ApiClient {
  constructor(
    private readonly base: string,
    private readonly onSignedOut: () => void = () => {},
  ) {}

  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const init: RequestInit = { method, credentials: 'same-origin', headers: {} };
    if (body !== undefined) {
      init.headers = { 'Content-Type': 'application/json' };
      init.body = JSON.stringify(body);
    }
    const resp = await fetch(this.base + path, init);
    if (resp.status === 204) return {} as T;
    let data: Record<string, unknown> = {};
    try {
      data = (await resp.json()) as Record<string, unknown>;
    } catch {
      /* an empty or non-JSON body */
    }
    if (!resp.ok) {
      if (resp.status === 401) this.onSignedOut();
      throw new Refused(resp.status, String(data['error'] ?? 'error'), String(data['message'] ?? t('cafard could not do that.')));
    }
    return data as T;
  }

  /**
   * send is a request whose body is not JSON: text, or a file, as `curl -T`
   * sends one. The answer is JSON like any other.
   */
  async send<T>(method: string, path: string, body: BodyInit, contentType?: string): Promise<T> {
    const init: RequestInit = { method, credentials: 'same-origin', body, headers: contentType ? { 'Content-Type': contentType } : {} };
    const resp = await fetch(this.base + path, init);
    let data: Record<string, unknown> = {};
    try {
      data = (await resp.json()) as Record<string, unknown>;
    } catch {
      /* no body */
    }
    if (!resp.ok) {
      if (resp.status === 401) this.onSignedOut();
      throw new Refused(resp.status, String(data['error'] ?? 'error'), String(data['message'] ?? t('cafard could not do that.')));
    }
    return data as T;
  }

  get<T>(path: string): Promise<T> {
    return this.request<T>('GET', path);
  }
  post<T>(path: string, body?: unknown): Promise<T> {
    return this.request<T>('POST', path, body);
  }
  patch<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>('PATCH', path, body);
  }
  put<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>('PUT', path, body);
  }
  delete<T>(path: string, body?: unknown): Promise<T> {
    return this.request<T>('DELETE', path, body);
  }
}
