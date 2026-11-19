// Formatting shared by the renderers. Each mirrors a Go function in
// internal/web/pages/views.go, so server-rendered rows and live rows read
// the same.

const SEV_LETTERS = ['M', 'A', 'C', 'E', 'W', 'N', 'I', 'D'] as const;
const SEV_NAMES = ['EMERG', 'ALERT', 'CRIT', 'ERR', 'WARN', 'NOTICE', 'INFO', 'DEBUG'] as const;

export function severityLetter(sev: number): string {
  return SEV_LETTERS[sev] ?? '?';
}

export function severityName(sev: number): string {
  return SEV_NAMES[sev] ?? String(sev);
}

const UTC_TIME = /T(\d\d:\d\d:\d\d)(?:\.(\d+))?Z$/;

/** "15:04:05.000" in UTC, like pages.LogTime. */
export function logTime(iso: string): string {
  const m = UTC_TIME.exec(iso);
  if (m !== null) return `${m[1]}.${(m[2] ?? '').padEnd(3, '0').slice(0, 3)}`;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toISOString().slice(11, 23);
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'] as const;

/** The UTC time today (up to minutes or seconds), else "Nov 2 15:04"; null when iso is no date. */
function todayOrDate(iso: string, nowMs: number, today: 'minutes' | 'seconds'): string | null {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return null;
  const s = d.toISOString();
  if (s.slice(0, 10) === new Date(nowMs).toISOString().slice(0, 10)) return s.slice(11, today === 'seconds' ? 19 : 16);
  return `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()} ${s.slice(11, 16)}`;
}

/** A last-activity time, like pages.Activity: the time today, else the date. */
export function activity(iso: string | undefined, nowMs: number): string {
  return (iso === undefined ? null : todayOrDate(iso, nowMs, 'seconds')) ?? 'never';
}

/** A claim chip, like pages.StreamCard: "codex until 10:45" (UTC). */
export function claimText(c: { readonly principal: string; readonly expires_at: string }): string {
  const d = new Date(c.expires_at);
  return Number.isNaN(d.getTime()) ? c.principal : `${c.principal} until ${d.toISOString().slice(11, 16)}`;
}

/** Lines per minute with one decimal, like strconv.FormatFloat(x, 'f', 1). */
export function rate(perMin: number): string {
  return perMin.toFixed(1);
}

// Track D: the chat strings. Each mirrors a Go function in
// internal/web/pages/chat_views.go.

/** A message time, like pages.MessageTime: "15:04" today (UTC), else the date. */
export function messageTime(iso: string, nowMs: number): string {
  return todayOrDate(iso, nowMs, 'minutes') ?? '';
}

/** A chat row's preview, like pages.PreviewText. */
export function previewText(p: { readonly sender?: string; readonly text: string }): string {
  return p.sender === undefined || p.sender === '' ? p.text : `${p.sender}: ${p.text}`;
}

/** What a message with images and no text shows as its preview, like messaging.ImagesLabel. */
export function imagesLabel(n: number): string {
  if (n === 0) return '';
  return n === 1 ? '[1 image]' : `[${n} images]`;
}

/** What a message that only points at gallery images shows as its preview, like messaging.GalleryImagesLabel. */
export function galleryImagesLabel(n: number): string {
  if (n === 0) return '';
  return n === 1 ? '[1 gallery image]' : `[${n} gallery images]`;
}

/** A line count, like pages.LinesText. */
export function linesText(n: number, openEnded: boolean): string {
  return `${n === 1 ? '1 line' : `${n} lines`}${openEnded ? ' so far' : ''}`;
}

/** Where an event row leads without the bundle, like pages.EventLink. */
export function eventLink(stream: string, seq: number): string {
  if (stream.startsWith('chan:') || stream.startsWith('dm:')) return '';
  return `/live?${new URLSearchParams({ streams: stream, to_seq: String(seq) }).toString()}`;
}

/** The page of a chat id, like pages.ChatHref. */
export function chatHref(id: string): string {
  if (id.startsWith('chan:')) {
    const [project = '', name = ''] = id.slice(5).split('/', 2);
    return `/c/${encodeURIComponent(project)}/${encodeURIComponent(name)}`;
  }
  if (id.startsWith('dm:')) return `/dm/${encodeURIComponent(id.slice(3))}`;
  return '/';
}

/** "#dut1" or "@codex", like pages.ChatLabel. */
export function chatLabel(id: string): string {
  if (id.startsWith('chan:')) return `#${id.slice(5)}`;
  if (id.startsWith('dm:')) return `@${id.slice(3)}`;
  return id;
}

/** The receipt line, like pages.SeenText. */
export function seenText(by: readonly string[] | undefined): string {
  return by === undefined || by.length === 0 ? '' : `seen by ${by.join(', ')}`;
}

/** An answered question's line, like pages.AnswerText. */
export function answerText(a: { readonly by: string; readonly choice?: string } | undefined): string {
  if (a === undefined) return '';
  return a.choice === undefined || a.choice === '' ? `answered by ${a.by}` : `answered by ${a.by}: ${a.choice}`;
}

/** A log attachment's range in /live, like pages.LogLink (keys sorted, as url.Values.Encode). */
export function logLink(a: {
  readonly streams: readonly string[];
  readonly from_seq: number;
  readonly to_seq: number | null;
  readonly min_severity?: number;
  readonly grep?: string;
}): string {
  const q: [string, string][] = [['from_seq', String(a.from_seq)]];
  if (a.grep !== undefined && a.grep !== '') q.push(['re', a.grep]);
  if (a.min_severity !== undefined) q.push(['sev', String(a.min_severity)]);
  q.push(['streams', a.streams.join(',')]);
  if (a.to_seq !== null) q.push(['to_seq', String(a.to_seq)]);
  return `/live?${new URLSearchParams(q).toString()}`;
}

// Galleries. Each mirrors a Go function in internal/messaging/gallery.go.

/** A gallery's page ("lab/shots"), on a section when one is given, like messaging.GalleryHref. */
export function galleryHref(gallery: string, section?: string): string {
  const [project = '', name = ''] = gallery.split('/', 2);
  const path = `/g/${encodeURIComponent(project)}/${encodeURIComponent(name)}`;
  return section === undefined ? path : `${path}?${new URLSearchParams({ section }).toString()}`;
}

/** The stable page of a gallery item, like messaging.ItemHref. */
export function itemHref(gallery: string, seq: number): string {
  return `${galleryHref(gallery)}/${seq}`;
}

/** A gallery item's title: the first line of its text, at most 140 characters, like messaging.previewText. */
export function itemTitle(text: string): string {
  let t = text.trim();
  const nl = t.search(/[\r\n]/);
  if (nl >= 0) t = `${t.slice(0, nl)} ...`;
  const runes = Array.from(t);
  return runes.length > 140 ? `${runes.slice(0, 140).join('')}...` : t;
}

/** What a message writes to show a gallery section, like messaging.SectionRef. */
export function sectionRef(gallery: string, section: string): string {
  return `[[${gallery}|${section}]]`;
}

/** A section's heading, like pages.SectionLabel. */
export function sectionLabel(name: string): string {
  return name === '' ? 'Unsorted' : name;
}

/** Where a chat's row leads: a gallery's grid, else the conversation, like pages.ChatRow. */
export function chatPageHref(chat: { readonly id: string; readonly channel_kind?: string }): string {
  return chat.channel_kind === 'gallery' ? galleryHref(chat.id.slice(5)) : chatHref(chat.id);
}
