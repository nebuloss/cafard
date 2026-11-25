import type { FileInfo, Message } from './types';

/** The images a bubble shows in place; anything else is a download (server's safeImages). */
export const SAFE_IMAGES = new Set(['image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif']);

export function isPhoto(f: FileInfo): boolean {
  return f.state === 'stored' && SAFE_IMAGES.has(f.type);
}

/** One bubble of the timeline: a message, or an album of several. */
export interface Item {
  messages: Message[];
}

/**
 * Albums gathers what an agent sends as one thing: it posts each image as a
 * message of its own (upload_link takes one file at a time), and a run of
 * them read as a column of single photos. A run of image-only messages
 * from one author, each within gap of the one before and on the same day,
 * is one item, drawn as one grid and viewed as one set.
 */
export class Albums {
  static readonly gap = 5 * 60_000;

  /** Whether m is photos and nothing else. */
  static photosOnly(m: Message): boolean {
    const files = m.files ?? [];
    return !m.deleted && !m.text?.trim() && files.length > 0 && files.every(isPhoto);
  }

  static group(messages: readonly Message[], gap = Albums.gap): Item[] {
    const out: Item[] = [];
    for (const m of messages) {
      const last = out[out.length - 1];
      const prev = last?.messages[last.messages.length - 1];
      if (
        last && prev &&
        Albums.photosOnly(m) && Albums.photosOnly(prev) &&
        prev.author.name === m.author.name &&
        new Date(m.at).getTime() - new Date(prev.at).getTime() <= gap &&
        new Date(m.at).toDateString() === new Date(prev.at).toDateString()
      ) {
        last.messages.push(m);
      } else {
        out.push({ messages: [m] });
      }
    }
    return out;
  }

  /** An item's photos in order, each with the message it came in. */
  static photos(item: Item): { file: FileInfo; message: Message }[] {
    return item.messages.flatMap((m) => (m.deleted ? [] : (m.files ?? []).filter(isPhoto).map((file) => ({ file, message: m }))));
  }
}
