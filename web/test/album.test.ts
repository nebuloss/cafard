import { describe, expect, it } from 'vitest';
import { Albums } from '../src/chat/album';
import type { FileInfo, Message } from '../src/chat/types';

const photo = (id: number): FileInfo => ({ id, name: `p${id}.png`, type: 'image/png', size: 1, state: 'stored', url: `/f/${id}` });
const log = (id: number): FileInfo => ({ id, name: 'boot.log', type: 'text/plain', size: 1, state: 'stored', url: `/f/${id}`, lines_url: `/f/${id}/lines` });
const msg = (id: number, who: string, at: string, extra: Partial<Message>): Message =>
  ({ id, author: { name: who, kind: 'agent' } as Message['author'], at, ...extra });

describe('Albums', () => {
  it('gathers a run of photo-only messages from one author', () => {
    const items = Albums.group([
      msg(1, 'dev-code', '2026-11-25T10:00:00Z', { text: 'here are the screenshots' }),
      msg(2, 'dev-code', '2026-11-25T10:00:05Z', { files: [photo(1)] }),
      msg(3, 'dev-code', '2026-11-25T10:00:09Z', { files: [photo(2), photo(3)] }),
      msg(4, 'dev-code', '2026-11-25T10:01:00Z', { files: [photo(4)] }),
      msg(5, 'guillaume', '2026-11-25T10:01:30Z', { files: [photo(5)] }),
      msg(6, 'dev-code', '2026-11-25T10:20:00Z', { files: [photo(6)] }),
      msg(7, 'dev-code', '2026-11-25T10:20:02Z', { files: [log(7)] }),
    ]);
    expect(items.map((i) => i.messages.map((m) => m.id))).toEqual([[1], [2, 3, 4], [5], [6], [7]]);
    expect(Albums.photos(items[1]!).map((p) => p.file.id)).toEqual([1, 2, 3, 4]);
    expect(Albums.photos(items[1]!)[2]!.message.id).toBe(3);
  });

  it('keeps a deleted message out of an album', () => {
    const items = Albums.group([
      msg(1, 'dev-code', '2026-11-25T10:00:00Z', { files: [photo(1)] }),
      msg(2, 'dev-code', '2026-11-25T10:00:01Z', { deleted: true }),
      msg(3, 'dev-code', '2026-11-25T10:00:02Z', { files: [photo(3)] }),
    ]);
    expect(items.length).toBe(3);
  });
});
