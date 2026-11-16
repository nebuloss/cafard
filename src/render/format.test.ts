import { describe, expect, it } from 'vitest';
import { activity, logTime, rate, severityLetter } from '@/render/format.ts';

describe('format', () => {
  it('shows times like the server', () => {
    expect(logTime('2026-11-16T09:41:02.512904417Z')).toBe('09:41:02.512');
    expect(logTime('2026-11-16T09:41:02Z')).toBe('09:41:02.000');
    expect(logTime('2026-11-16T10:41:02.5+01:00')).toBe('09:41:02.500');
  });

  it('shows activity as a time today and a date before', () => {
    const now = Date.parse('2026-11-16T12:00:00Z');
    expect(activity('2026-11-16T09:42:16.33Z', now)).toBe('09:42:16');
    expect(activity('2026-11-02T14:05:33Z', now)).toBe('Nov 2 14:05');
    expect(activity(undefined, now)).toBe('never');
  });

  it('keeps one decimal and a letter per severity', () => {
    expect(rate(42.66)).toBe('42.7');
    expect(severityLetter(3)).toBe('E');
    expect(severityLetter(9)).toBe('?');
  });
});
