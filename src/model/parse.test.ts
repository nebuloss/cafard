import { describe, expect, it } from 'vitest';
import {
  parseAttachment,
  parseMarkCreated,
  parseCounts,
  parseEntry,
  parseEntryPage,
  parseError,
  parseMe,
  parsePairing,
  parsePrincipal,
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
};

// Fixtures the bundle of track C does not read: request bodies, and the DTOs
// of the chat pages, whose parsers come with track D.
const notYet = new Set(['answered', 'seen', 'chat_channel', 'chat_direct', 'overview', 'share', 'channel_create', 'channel_update', 'send_request', 'log_accepted', 'token_create', 'pairing_create', 'mark_create']);

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
