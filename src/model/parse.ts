import { ApiError } from '@/net/ApiError.ts';
import type {
  Answer,
  Attachment,
  ChatCount,
  Claim,
  Counts,
  Entry,
  EntryKind,
  ErrorBody,
  LineState,
  MarkCreated,
  Me,
  Page,
  Pairing,
  Principal,
  PrincipalKind,
  Question,
  Reset,
  Scope,
  Segment,
  Sender,
  Session,
  Snippet,
  SnippetLanguage,
  Stream,
  StreamState,
  Theme,
  Token,
  TokenCreated,
} from '@/model/types.ts';

/**
 * Runtime validation of server JSON (PLAN 9.4 rule 7): a value that does not
 * have the shape the bundle expects becomes an ApiError here, never an
 * undefined deep in the UI. Unknown fields are ignored, so the server can add
 * fields without breaking older bundles.
 */

type Obj = Readonly<Record<string, unknown>>;

function fail(path: string, want: string): never {
  throw new ApiError('bad_response', `The server sent something unexpected (${path} is not ${want}).`, 0);
}

function obj(raw: unknown, path: string): Obj {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) fail(path, 'an object');
  return raw as Obj;
}

function str(o: Obj, key: string, path: string): string {
  const v = o[key];
  if (typeof v !== 'string') fail(`${path}.${key}`, 'a string');
  return v;
}

function optStr(o: Obj, key: string, path: string): string | undefined {
  return o[key] === undefined || o[key] === null ? undefined : str(o, key, path);
}

function num(o: Obj, key: string, path: string): number {
  const v = o[key];
  if (typeof v !== 'number' || !Number.isFinite(v)) fail(`${path}.${key}`, 'a number');
  return v;
}

function optNum(o: Obj, key: string, path: string): number | undefined {
  return o[key] === undefined || o[key] === null ? undefined : num(o, key, path);
}

function bool(o: Obj, key: string, path: string): boolean {
  const v = o[key];
  if (typeof v !== 'boolean') fail(`${path}.${key}`, 'a boolean');
  return v;
}

function optBool(o: Obj, key: string, path: string): boolean | undefined {
  return o[key] === undefined || o[key] === null ? undefined : bool(o, key, path);
}

function oneOf<T extends string>(o: Obj, key: string, path: string, allowed: readonly T[]): T {
  const v = str(o, key, path);
  if (!(allowed as readonly string[]).includes(v)) fail(`${path}.${key}`, allowed.join(' | '));
  return v as T;
}

function arr<T>(o: Obj, key: string, path: string, item: (raw: unknown, path: string) => T): T[] {
  const v = o[key];
  if (!Array.isArray(v)) fail(`${path}.${key}`, 'an array');
  return v.map((x, i) => item(x, `${path}.${key}[${i}]`));
}

function optArr<T>(o: Obj, key: string, path: string, item: (raw: unknown, path: string) => T): T[] | undefined {
  return o[key] === undefined || o[key] === null ? undefined : arr(o, key, path, item);
}

const asString = (raw: unknown, path: string): string => {
  if (typeof raw !== 'string') fail(path, 'a string');
  return raw;
};

/** Copies only the keys that are present, so optional fields stay absent. */
function compact<T extends object>(value: T): T {
  const record = value as Record<string, unknown>;
  for (const key of Object.keys(record)) {
    if (record[key] === undefined) delete record[key];
  }
  return value;
}

const KINDS: readonly PrincipalKind[] = ['agent', 'human', 'device'];
const SCOPES: readonly Scope[] = ['ingest', 'read', 'message', 'serial_write', 'admin'];
const ENTRY_KINDS: readonly EntryKind[] = ['line', 'marker', 'message'];
const STATES: readonly StreamState[] = ['connected', 'idle', 'down'];
const THEMES: readonly Theme[] = ['system', 'light', 'dark'];
const LANGUAGES: readonly SnippetLanguage[] = ['shell', 'toml', 'json', 'env'];

const asScope = (raw: unknown, path: string): Scope => {
  const s = asString(raw, path);
  if (!(SCOPES as readonly string[]).includes(s)) fail(path, 'a scope');
  return s as Scope;
};

export function parseError(raw: unknown): ErrorBody {
  const e = obj(obj(raw, 'body').error, 'error');
  return compact({ code: str(e, 'code', 'error'), message: str(e, 'message', 'error'), retry_after_s: optNum(e, 'retry_after_s', 'error') });
}

export function parseMe(raw: unknown): Me {
  const o = obj(raw, 'me');
  return compact({
    name: str(o, 'name', 'me'),
    kind: oneOf(o, 'kind', 'me', KINDS),
    scopes: arr(o, 'scopes', 'me', asScope),
    session_id: optNum(o, 'session_id', 'me'),
    theme: oneOf(o, 'theme', 'me', THEMES),
  });
}

export function parsePrincipal(raw: unknown, path = 'principal'): Principal {
  const o = obj(raw, path);
  return compact({ name: str(o, 'name', path), kind: oneOf(o, 'kind', path, KINDS), last_seen: optStr(o, 'last_seen', path) });
}

function parseSegment(raw: unknown, path: string): Segment {
  const o = obj(raw, path);
  const t = oneOf(o, 't', path, ['text', 'code', 'codeblock', 'link'] as const);
  const v = str(o, 'v', path);
  switch (t) {
    case 'link':
      return { t, v, href: str(o, 'href', path) };
    case 'codeblock':
      return compact({ t, v, lang: optStr(o, 'lang', path) });
    default:
      return { t, v };
  }
}

export function parseAttachment(raw: unknown, path: string): Attachment {
  const o = obj(raw, path);
  const type = oneOf(o, 'type', path, ['image', 'log'] as const);
  if (type === 'image') {
    return compact({
      type,
      sha256: str(o, 'sha256', path),
      mime: str(o, 'mime', path),
      width: optNum(o, 'width', path),
      height: optNum(o, 'height', path),
      url: str(o, 'url', path),
      thumb_url: optStr(o, 'thumb_url', path),
      preview: bool(o, 'preview', path),
    });
  }
  return compact({
    type,
    stream: optStr(o, 'stream', path),
    channel: optStr(o, 'channel', path),
    streams: arr(o, 'streams', path, asString),
    from_seq: num(o, 'from_seq', path),
    to_seq: o['to_seq'] === null ? null : num(o, 'to_seq', path),
    grep: optStr(o, 'grep', path),
    min_severity: optNum(o, 'min_severity', path),
    title: optStr(o, 'title', path),
    lines: num(o, 'lines', path),
    first_ts: optStr(o, 'first_ts', path),
    last_ts: optStr(o, 'last_ts', path),
    snapshot: bool(o, 'snapshot', path),
    expired: bool(o, 'expired', path),
    preview: arr(o, 'preview', path, parseEntry),
  });
}

function parseSender(raw: unknown, path: string): Sender {
  const o = obj(raw, path);
  return { name: str(o, 'name', path), kind: oneOf(o, 'kind', path, KINDS) };
}

function parseAnswer(raw: unknown, path: string): Answer {
  const o = obj(raw, path);
  return compact({ by: str(o, 'by', path), choice: optStr(o, 'choice', path), seq: num(o, 'seq', path) });
}

function parseQuestion(raw: unknown, path: string): Question {
  const o = obj(raw, path);
  return compact({
    choices: arr(o, 'choices', path, asString),
    open: bool(o, 'open', path),
    answer: o['answer'] === undefined ? undefined : parseAnswer(o['answer'], `${path}.answer`),
  });
}

export function parseEntry(raw: unknown, path = 'entry'): Entry {
  const o = obj(raw, path);
  const meta = o['meta'];
  return compact({
    seq: num(o, 'seq', path),
    ts: str(o, 'ts', path),
    src_ts: optStr(o, 'src_ts', path),
    stream: str(o, 'stream', path),
    kind: oneOf(o, 'kind', path, ENTRY_KINDS),
    severity: num(o, 'severity', path),
    host: optStr(o, 'host', path),
    app: optStr(o, 'app', path),
    text: str(o, 'text', path),
    meta: meta === undefined ? undefined : obj(meta, `${path}.meta`),
    chat: optStr(o, 'chat', path),
    sender: o['sender'] === undefined ? undefined : parseSender(o['sender'], `${path}.sender`),
    recipient: optStr(o, 'recipient', path),
    reply_to: optNum(o, 'reply_to', path),
    segments: optArr(o, 'segments', path, parseSegment),
    attachments: optArr(o, 'attachments', path, parseAttachment),
    seen_by: optArr(o, 'seen_by', path, asString),
    question: o['question'] === undefined ? undefined : parseQuestion(o['question'], `${path}.question`),
    event: optBool(o, 'event', path),
  });
}

function parseClaim(raw: unknown, path: string): Claim {
  const o = obj(raw, path);
  return compact({ principal: str(o, 'principal', path), note: optStr(o, 'note', path), expires_at: str(o, 'expires_at', path) });
}

function parseLineState(raw: unknown, path: string): LineState {
  const o = obj(raw, path);
  return compact({ dtr: optBool(o, 'dtr', path), rts: optBool(o, 'rts', path), cts: optBool(o, 'cts', path), dcd: optBool(o, 'dcd', path) });
}

export function parseStream(raw: unknown, path = 'stream'): Stream {
  const o = obj(raw, path);
  const labelsRaw = obj(o['labels'], `${path}.labels`);
  const labels: Record<string, string> = {};
  for (const [k, v] of Object.entries(labelsRaw)) labels[k] = asString(v, `${path}.labels.${k}`);
  return compact({
    id: str(o, 'id', path),
    kind: str(o, 'kind', path),
    transport: str(o, 'transport', path),
    labels,
    status: oneOf(o, 'status', path, STATES),
    last_seq: num(o, 'last_seq', path),
    last_activity: optStr(o, 'last_activity', path),
    rate_per_min: num(o, 'rate_per_min', path),
    dropped: num(o, 'dropped', path),
    claim: o['claim'] === undefined ? undefined : parseClaim(o['claim'], `${path}.claim`),
    line_state: o['line_state'] === undefined ? undefined : parseLineState(o['line_state'], `${path}.line_state`),
    last_line: optStr(o, 'last_line', path),
  });
}

function parseChatCount(raw: unknown, path: string): ChatCount {
  const o = obj(raw, path);
  return { id: str(o, 'id', path), unread: num(o, 'unread', path), open_questions: num(o, 'open_questions', path) };
}

export function parseCounts(raw: unknown): Counts {
  const o = obj(raw, 'counts');
  return {
    open_questions: num(o, 'open_questions', 'counts'),
    unread_direct: num(o, 'unread_direct', 'counts'),
    sources_down: num(o, 'sources_down', 'counts'),
    chats: arr(o, 'chats', 'counts', parseChatCount),
  };
}

export function parseToken(raw: unknown, path = 'token'): Token {
  const o = obj(raw, path);
  return compact({
    id: num(o, 'id', path),
    label: str(o, 'label', path),
    principal: str(o, 'principal', path),
    kind: oneOf(o, 'kind', path, KINDS),
    scopes: arr(o, 'scopes', path, asScope),
    created_at: str(o, 'created_at', path),
    last_used: optStr(o, 'last_used', path),
  });
}

function parseSnippet(raw: unknown, path: string): Snippet {
  const o = obj(raw, path);
  return { label: str(o, 'label', path), language: oneOf(o, 'language', path, LANGUAGES), text: str(o, 'text', path) };
}

export function parseTokenCreated(raw: unknown): TokenCreated {
  const o = obj(raw, 'token_created');
  return { ...parseToken(raw, 'token_created'), token: str(o, 'token', 'token_created'), snippets: arr(o, 'snippets', 'token_created', parseSnippet) };
}

export function parseSession(raw: unknown, path = 'session'): Session {
  const o = obj(raw, path);
  return compact({
    id: num(o, 'id', path),
    label: str(o, 'label', path),
    principal: str(o, 'principal', path),
    user_agent: optStr(o, 'user_agent', path),
    created_at: str(o, 'created_at', path),
    expires_at: str(o, 'expires_at', path),
    current: bool(o, 'current', path),
  });
}

export function parsePairing(raw: unknown): Pairing {
  const o = obj(raw, 'pairing');
  return compact({
    id: num(o, 'id', 'pairing'),
    url: str(o, 'url', 'pairing'),
    qr_url: str(o, 'qr_url', 'pairing'),
    expires_at: str(o, 'expires_at', 'pairing'),
    principal: optStr(o, 'principal', 'pairing'),
    new_name: optStr(o, 'new_name', 'pairing'),
  });
}

export function parsePage<T>(item: (raw: unknown, path: string) => T): (raw: unknown) => Page<T> {
  return (raw) => {
    const o = obj(raw, 'page');
    return compact({
      items: arr(o, 'items', 'page', item),
      has_more: bool(o, 'has_more', 'page'),
      next_seq: optNum(o, 'next_seq', 'page'),
      next_before_seq: optNum(o, 'next_before_seq', 'page'),
      focus_seq: optNum(o, 'focus_seq', 'page'),
    });
  };
}

export const parseEntryPage = parsePage(parseEntry);

export function parseReset(raw: unknown): Reset {
  return { reason: str(obj(raw, 'reset'), 'reason', 'reset') };
}

export function parseMarkCreated(raw: unknown): MarkCreated {
  return { seq: num(obj(raw, 'mark'), 'seq', 'mark') };
}

/** A {key: T[]} wrapper, as /api/streams and friends answer. */
export function parseList<T>(key: string, item: (raw: unknown, path: string) => T): (raw: unknown) => T[] {
  return (raw) => arr(obj(raw, 'body'), key, 'body', item);
}
