import type { HttpClient } from '@/net/HttpClient.ts';
import { parseAcked, parseChat, parseEntryPage, parseList, parseMarkCreated, parseStream, parseTokenCreated } from '@/model/parse.ts';
import type { Acked, ChannelCreate, ChannelUpdate, Chat, ChatPrefs, Entry, MarkCreated, Page, Stream, TokenCreate, TokenCreated } from '@/model/types.ts';

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

/** The /live query as an /api/entries query. */
export function entriesQuery(query: string): EntriesQuery {
  const p = new URLSearchParams(query);
  const get = (k: string): string | undefined => p.get(k) ?? undefined;
  const num = (k: string): number | undefined => {
    const v = p.get(k);
    return v === null || v === '' || Number.isNaN(Number(v)) ? undefined : Number(v);
  };
  return {
    streams: get('streams'),
    channel: get('channel'),
    sev: get('sev'),
    re: get('re'),
    kinds: get('kinds'),
    from_seq: num('from_seq'),
    to_seq: num('to_seq'),
  };
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

  createToken(request: TokenCreate): Promise<TokenCreated> {
    return this.http.request('POST', '/api/tokens', { body: request, parse: parseTokenCreated });
  }

  // Track D: chats. Sending a message goes through the upload queue.

  chats(options: { all?: boolean; archived?: boolean } = {}): Promise<Chat[]> {
    const query = { all: options.all === true ? 1 : undefined, archived: options.archived === true ? 1 : undefined };
    return this.http.request('GET', '/api/chats', { query, parse: parseList('chats', parseChat) });
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

  createChannel(request: ChannelCreate): Promise<Chat> {
    return this.http.request('POST', '/api/channels', { body: request, parse: (raw) => parseChat(raw) });
  }

  updateChannel(name: string, update: ChannelUpdate): Promise<Chat> {
    return this.http.request('PATCH', `/api/channels/${encodeURIComponent(name)}`, { body: update, parse: (raw) => parseChat(raw) });
  }

  deleteChannel(name: string): Promise<void> {
    return this.http.request('DELETE', `/api/channels/${encodeURIComponent(name)}`, { query: { confirm: name } });
  }
}
