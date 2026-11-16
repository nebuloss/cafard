import type { HttpClient } from '@/net/HttpClient.ts';
import {
  parseAcked,
  parseChat,
  parseEntry,
  parseOverview,
  parseShare,
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
  Acked,
  ChannelCreate,
  ChannelUpdate,
  Chat,
  ChatPrefs,
  Overview,
  Share,
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

/** A page of a chat's timeline (CONTRACTS.md 2.3). */
export interface TimelineQuery {
  readonly before_seq?: number;
  readonly since_seq?: number;
  readonly limit?: number;
  readonly f?: string;
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

  // Track D: chats. Sending a message goes through the upload queue.

  chats(options: { all?: boolean; archived?: boolean } = {}): Promise<Chat[]> {
    const query = { all: options.all === true ? 1 : undefined, archived: options.archived === true ? 1 : undefined };
    return this.http.request('GET', '/api/chats', { query, parse: parseList('chats', parseChat) });
  }

  overview(): Promise<Overview> {
    return this.http.request('GET', '/api/overview', { parse: parseOverview });
  }

  timeline(chat: string, query: TimelineQuery = {}): Promise<Page<Entry>> {
    return this.http.request('GET', `/api/chats/${encodeURIComponent(chat)}/timeline`, { query: { ...query }, parse: parseEntryPage });
  }

  setPrefs(chat: string, prefs: ChatPrefs): Promise<Chat> {
    return this.http.request('PUT', `/api/chats/${encodeURIComponent(chat)}/prefs`, { body: prefs, parse: (raw) => parseChat(raw) });
  }

  markRead(chat: string, upToSeq: number): Promise<Acked> {
    return this.http.request('POST', `/api/chats/${encodeURIComponent(chat)}/read`, { body: { up_to_seq: upToSeq }, parse: parseAcked });
  }

  ack(seqs: readonly number[]): Promise<Acked> {
    return this.http.request('POST', '/api/acks', { body: { seqs }, parse: parseAcked });
  }

  message(seq: number): Promise<Entry> {
    return this.http.request('GET', `/api/messages/${seq}`, { parse: (raw) => parseEntry(raw) });
  }

  createChannel(request: ChannelCreate): Promise<Chat> {
    return this.http.request('POST', '/api/channels', { body: request, parse: (raw) => parseChat(raw) });
  }

  updateChannel(name: string, update: ChannelUpdate): Promise<Chat> {
    return this.http.request('PATCH', `/api/channels/${encodeURIComponent(name)}`, { body: update, parse: (raw) => parseChat(raw) });
  }

  deleteChannel(name: string): Promise<void> {
    return this.http.request('DELETE', `/api/channels/${encodeURIComponent(name)}`, { query: { confirm: name } });
  }

  share(id: string): Promise<Share> {
    return this.http.request('GET', `/api/shares/${encodeURIComponent(id)}`, { parse: parseShare });
  }
}
