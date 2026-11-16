import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@/net/ApiError.ts';
import { CafardApi } from '@/net/CafardApi.ts';
import { FetchHttpClient, type FetchFn } from '@/net/HttpClient.ts';
import { fixture } from '@/testing/pages.ts';

function respond(status: number, body: unknown): FetchFn {
  return vi.fn(async () => new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
}

describe('HttpClient', () => {
  it('sends X-Cafard, same-origin credentials and a JSON body', async () => {
    const fetchFn = respond(201, fixture('token_created'));
    const api = new CafardApi(new FetchHttpClient(fetchFn));
    const created = await api.createToken({ principal: 'claude-laptop', label: 'laptop', scopes: ['read', 'message'] });
    expect(created.token.startsWith('cfd_')).toBe(true);
    const [url, init] = (fetchFn as ReturnType<typeof vi.fn>).mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/tokens');
    expect((init.headers as Record<string, string>)['X-Cafard']).toBe('1');
    expect(init.credentials).toBe('same-origin');
    expect(JSON.parse(String(init.body))).toEqual({ principal: 'claude-laptop', label: 'laptop', scopes: ['read', 'message'] });
  });

  it('builds queries and skips empty values', async () => {
    const fetchFn = respond(200, fixture('page_entry'));
    await new CafardApi(new FetchHttpClient(fetchFn)).entries({ streams: 'serial:*', sev: '', limit: 10 });
    expect((fetchFn as ReturnType<typeof vi.fn>).mock.calls[0]?.[0]).toBe('/api/entries?streams=serial%3A*&limit=10');
  });

  it('turns the error DTO into an ApiError with the server sentence', async () => {
    const http = new FetchHttpClient(respond(429, fixture('error_rate_limited')));
    const error = await http.request('GET', '/api/me').catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('rate_limited');
    expect((error as ApiError).message).toBe('You sent 30 messages in the last minute; wait 12 s.');
    expect((error as ApiError).retryAfterS).toBe(12);
    expect((error as ApiError).status).toBe(429);
  });

  it('reports an unreachable server and a non-DTO error', async () => {
    const offline = new FetchHttpClient(async () => {
      throw new TypeError('network');
    });
    await expect(offline.request('GET', '/api/me')).rejects.toMatchObject({ code: 'offline' });
    await expect(new FetchHttpClient(respond(502, 'Bad Gateway')).request('GET', '/api/me')).rejects.toMatchObject({ code: 'http_error', status: 502 });
  });

  it('answers 204 with undefined and refuses other origins', async () => {
    await expect(new FetchHttpClient(respond(204, undefined)).request('DELETE', '/api/tokens/3')).resolves.toBeUndefined();
    await expect(new FetchHttpClient(respond(200, {})).request('GET', 'https://evil.example/api')).rejects.toThrow();
  });
});
