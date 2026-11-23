/**
 * The chat app's paths. A conversation is its address, as in the API and in
 * curl's: /be98/wifi-drops. So a link to a conversation is one to copy, and
 * Back goes where it went.
 *
 *   /                      the conversations
 *   /<project>/<name>      one conversation
 *   /new                   a new conversation, its project to choose
 *   /new/<project>         a new conversation in that project
 *
 * A project's name is the first part of a path, so the names the public
 * listener's own paths take are refused for projects (service/access).
 *
 * Pure: string in, string out, tested under node.
 */

export type ChatPlace =
  | { kind: 'list' }
  | { kind: 'conversation'; project: string; name: string }
  | { kind: 'new'; project?: string };

function segments(pathname: string): string[] {
  return pathname
    .split('/')
    .filter((s) => s !== '')
    .map((s) => {
      try {
        return decodeURIComponent(s);
      } catch {
        return s;
      }
    });
}

/** What a path shows; one that names nothing shows the list. */
export function placeOf(pathname: string): ChatPlace {
  const [a, b, c] = segments(pathname);
  if (a === undefined || c !== undefined) return { kind: 'list' };
  if (a === 'new') return b === undefined ? { kind: 'new' } : { kind: 'new', project: b };
  if (b === undefined) return { kind: 'list' };
  return { kind: 'conversation', project: a, name: b };
}

/** The path of a place. */
export function pathOf(place: ChatPlace): string {
  switch (place.kind) {
    case 'list':
      return '/';
    case 'new':
      return place.project ? `/new/${encodeURIComponent(place.project)}` : '/new';
    case 'conversation':
      return `/${encodeURIComponent(place.project)}/${encodeURIComponent(place.name)}`;
  }
}

/** The path a link from before paths meant: #c/be98/x, #new, #new/be98. */
export function fromHash(hash: string): string | null {
  let h = hash.replace(/^#/, '');
  try {
    h = decodeURIComponent(h);
  } catch {
    // kept as it came
  }
  if (h === '') return null;
  if (h === 'new') return '/new';
  if (h.startsWith('new/')) return pathOf({ kind: 'new', project: h.slice(4) });
  if (h.startsWith('c/')) {
    const [project, name] = h.slice(2).split('/');
    if (project && name) return pathOf({ kind: 'conversation', project, name });
  }
  return '/';
}
