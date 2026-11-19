import { describe, expect, it } from 'vitest';
import {
  parseAcked,
  parseAnswered,
  parseAttachment,
  parseChat,
  parseOverview,
  parseSeen,
  parseShare,
  parseMarkCreated,
  parseCounts,
  parseEntry,
  parseEntryPage,
  parseGalleryPage,
  parseError,
  parseMe,
  parsePairing,
  parsePrincipal,
  parseProject,
  parseReset,
  parseSession,
  parseStream,
  parseToken,
  parseTokenCreated,
} from '@/model/parse.ts';
import { ApiError } from '@/net/ApiError.ts';
import { fixture, fixtureNames } from '@/testing/pages.ts';

const parsers: Record<string, (raw: unknown) => unknown> = {
  me: parseMe,
  mark_created: parseMarkCreated,
  principal: (raw) => parsePrincipal(raw),
  entry_line: (raw) => parseEntry(raw),
  entry_marker: (raw) => parseEntry(raw),
  entry_message: (raw) => parseEntry(raw),
  entry_question: (raw) => parseEntry(raw),
  attachment_image: (raw) => parseAttachment(raw, 'attachment'),
  attachment_log: (raw) => parseAttachment(raw, 'attachment'),
  stream: (raw) => parseStream(raw),
  counts: parseCounts,
  token: (raw) => parseToken(raw),
  token_created: parseTokenCreated,
  session: (raw) => parseSession(raw),
  pairing: parsePairing,
  page_entry: parseEntryPage,
  reset: parseReset,
  chat_channel: (raw) => parseChat(raw),
  chat_direct: (raw) => parseChat(raw),
  chat_gallery: (raw) => parseChat(raw),
  entry_gallery_item: (raw) => parseEntry(raw),
  gallery_page: parseGalleryPage,
  overview: parseOverview,
  share: parseShare,
  seen: parseSeen,
  answered: parseAnswered,
  acked: parseAcked,
  project: (raw) => parseProject(raw),
};

// Fixtures the bundle never reads: request bodies, and what only programs
// receive.
const notYet = new Set([
  'channel_create', 'channel_create_gallery', 'channel_update', 'send_request', 'send_request_gallery', 'log_accepted', 'stream_list', 'token_create', 'pairing_create', 'mark_create',
  'chat_prefs', 'chat_read', 'ack_request',
]);

describe('parse', () => {
  it('reads every Go fixture back to the same value', () => {
    for (const name of fixtureNames()) {
      if (name.startsWith('error_')) {
        const raw = fixture(name) as { error: unknown };
        expect(parseError(raw), name).toEqual(raw.error);
        continue;
      }
      if (notYet.has(name)) continue;
      const parse = parsers[name];
      expect(parse, `no parser for fixture ${name}`).toBeDefined();
      const raw = fixture(name);
      expect(parse?.(raw), name).toEqual(raw);
    }
  });

  it('turns a wrong shape into an ApiError, not an undefined', () => {
    const bad = { ...(fixture('entry_line') as object), seq: 'nope' };
    expect(() => parseEntry(bad)).toThrow(ApiError);
    try {
      parseStream({ ...(fixture('stream') as object), status: 'exploded' });
    } catch (error) {
      expect(error).toBeInstanceOf(ApiError);
      expect((error as ApiError).code).toBe('bad_response');
    }
  });

  it('ignores fields it does not know', () => {
    const me = parseMe({ ...(fixture('me') as object), added_later: 1 });
    expect('added_later' in me).toBe(false);
  });
});
