import { describe, expect, it } from 'vitest';
import { streamTone } from '@/render/StreamTone.ts';

describe('streamTone', () => {
  // The same ids are pinned in internal/web/view/view_test.go.
  it('matches the server', () => {
    expect(streamTone('serial:dut1')).toBe(5);
    expect(streamTone('syslog:bench-pi')).toBe(4);
    expect(streamTone('push:laptop')).toBe(5);
    expect(streamTone('')).toBe(5);
  });
});
