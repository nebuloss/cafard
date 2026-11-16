import type { HttpClient } from '@/net/HttpClient.ts';
import {
  parseEntryPage,
  parseList,
  parseMarkCreated,
  parseMe,
  parsePairing,
  parsePrincipal,
  parseSession,
  parseStream,
  parseToken,
  parseTokenCreated,
} from '@/model/parse.ts';
import type {
  Entry,
  MarkCreated,
  Me,
  Page,
  Pairing,
  PairingCreate,
  Principal,
  PrincipalKind,
  Session,
  Stream,
  Token,
  TokenCreate,
  TokenCreated,
} from '@/model/types.ts';

/** The logs query of /live and /api/entries (PLAN 9.6.4). */
export interface EntriesQuery {
  readonly streams?: string;
  readonly channel?: string;
  readonly since_seq?: number;
  readonly before_seq?: number;
  readonly from_seq?: number;
  readonly to_seq?: number;
  readonly sev?: string;
  readonly re?: string;
  readonly kinds?: string;
  readonly limit?: number;
}

/**
 * Gateway over the JSON API (CONTRACTS.md 2.3): one method per endpoint,
 * every answer validated. Track A and D add their endpoints here.
 */
export class CafardApi {
  constructor(private readonly http: HttpClient) {}

  me(): Promise<Me> {
    return this.http.request('GET', '/api/me', { parse: parseMe });
  }

  principals(kind?: PrincipalKind): Promise<Principal[]> {
    return this.http.request('GET', '/api/principals', { query: { kind }, parse: parseList('principals', parsePrincipal) });
  }

  streams(channel?: string): Promise<Stream[]> {
    return this.http.request('GET', '/api/streams', { query: { channel }, parse: parseList('streams', parseStream) });
  }

  matchStreams(pattern: string): Promise<Stream[]> {
    return this.http.request('GET', '/api/streams/match', { query: { pattern }, parse: parseList('streams', parseStream) });
  }

  entries(query: EntriesQuery): Promise<Page<Entry>> {
    return this.http.request('GET', '/api/entries', { query: { ...query }, parse: parseEntryPage });
  }

  context(seq: number, before = 20, after = 20): Promise<Page<Entry>> {
    return this.http.request('GET', `/api/entries/${seq}/context`, { query: { before, after }, parse: parseEntryPage });
  }

  mark(stream: string, label: string): Promise<MarkCreated> {
    return this.http.request('POST', '/api/marks', { body: { stream, label }, parse: parseMarkCreated });
  }

  tokens(): Promise<Token[]> {
    return this.http.request('GET', '/api/tokens', { parse: parseList('tokens', parseToken) });
  }

  createToken(request: TokenCreate): Promise<TokenCreated> {
    return this.http.request('POST', '/api/tokens', { body: request, parse: parseTokenCreated });
  }

  revokeToken(id: number): Promise<void> {
    return this.http.request('DELETE', `/api/tokens/${id}`);
  }

  sessions(): Promise<Session[]> {
    return this.http.request('GET', '/api/sessions', { parse: parseList('sessions', parseSession) });
  }

  revokeSession(id: number): Promise<void> {
    return this.http.request('DELETE', `/api/sessions/${id}`);
  }

  createPairing(request: PairingCreate): Promise<Pairing> {
    return this.http.request('POST', '/api/pairings', { body: request, parse: parsePairing });
  }
}
