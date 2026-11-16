import { h, type Child } from '@/core/Dom.ts';
import type { Segment } from '@/model/types.ts';

/**
 * Renders the server-parsed segments of a message (CONTRACTS.md 6.4): text
 * as text nodes, code as <code>, code blocks as <pre><code>, links as
 * <a target="_blank">. The browser never parses message text itself, and a
 * link's href was checked by the server (http or https); h() refuses
 * anything else again.
 */
export function textSegments(segments: readonly Segment[] | undefined, fallback = ''): Child[] {
  if (segments === undefined || segments.length === 0) return fallback === '' ? [] : [fallback];
  return segments.map((s): Child => {
    switch (s.t) {
      case 'code':
        return h('code', {}, s.v);
      case 'codeblock':
        return h('pre', { class: 'mono', 'data-lang': s.lang ?? '' }, h('code', {}, s.v));
      case 'link':
        return /^https?:\/\//i.test(s.href) ? h('a', { href: s.href, rel: 'noopener noreferrer', target: '_blank' }, s.v) : s.v;
      default:
        return s.v;
    }
  });
}
