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

/** A last-activity time, like pages.Activity: the time today, else the date. */
export function activity(iso: string | undefined, nowMs: number): string {
  if (iso === undefined) return 'never';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return 'never';
  const now = new Date(nowMs);
  const s = d.toISOString();
  if (s.slice(0, 10) === now.toISOString().slice(0, 10)) return s.slice(11, 19);
  return `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()} ${s.slice(11, 16)}`;
}

/** Lines per minute with one decimal, like strconv.FormatFloat(x, 'f', 1). */
export function rate(perMin: number): string {
  return perMin.toFixed(1);
}

// Track D: the chat strings. Each mirrors a Go function in
// internal/web/pages/chat_views.go.

/** A message time, like pages.MessageTime: "15:04" today (UTC), else the date. */
export function messageTime(iso: string, nowMs: number): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const s = d.toISOString();
  if (s.slice(0, 10) === new Date(nowMs).toISOString().slice(0, 10)) return s.slice(11, 16);
  return `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()} ${s.slice(11, 16)}`;
}

/** A chat row's preview, like pages.PreviewText. */
export function previewText(p: { readonly sender?: string; readonly text: string }): string {
  return p.sender === undefined || p.sender === '' ? p.text : `${p.sender}: ${p.text}`;
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
  if (id.startsWith('chan:')) return `/c/${encodeURIComponent(id.slice(5))}`;
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
