/**
 * The colour of a stream tag: FNV-1a (32 bit) of the id's UTF-8 bytes,
 * mod 8. internal/web/view.Tone computes the same, so server-rendered and
 * live lines agree; both sides pin the same ids in a test.
 */
export function streamTone(stream: string): number {
  let hash = 0x811c9dc5;
  for (const byte of new TextEncoder().encode(stream)) {
    hash ^= byte;
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash % 8;
}
