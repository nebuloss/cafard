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
