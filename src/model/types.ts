// DTOs mirroring the Go JSON (internal/dto, CONTRACTS.md 3.3). Read-only:
// the bundle never mutates what the server sent. Built only by parse.ts.

export type PrincipalKind = 'agent' | 'human' | 'device';
export type Scope = 'ingest' | 'read' | 'message' | 'serial_write' | 'admin';
export type Theme = 'system' | 'light' | 'dark';
export type EntryKind = 'line' | 'marker' | 'message';
export type StreamState = 'connected' | 'idle' | 'down';

export interface Me {
  readonly name: string;
  readonly kind: PrincipalKind;
  readonly scopes: readonly Scope[];
  readonly session_id?: number;
  readonly theme: Theme;
}

export interface Principal {
  readonly name: string;
  readonly kind: PrincipalKind;
  readonly last_seen?: string;
}

export type Segment =
  | { readonly t: 'text'; readonly v: string }
  | { readonly t: 'code'; readonly v: string }
  | { readonly t: 'codeblock'; readonly v: string; readonly lang?: string }
  | { readonly t: 'link'; readonly v: string; readonly href: string };

export interface ImageAttachment {
  readonly type: 'image';
  readonly sha256: string;
  readonly mime: string;
  readonly width?: number;
  readonly height?: number;
  readonly url: string;
  readonly thumb_url?: string;
  readonly preview: boolean;
}

export interface LogAttachment {
  readonly type: 'log';
  readonly stream?: string;
  readonly channel?: string;
  readonly streams: readonly string[];
  readonly from_seq: number;
  readonly to_seq: number | null;
  readonly grep?: string;
  readonly min_severity?: number;
  readonly title?: string;
  readonly lines: number;
  readonly first_ts?: string;
  readonly last_ts?: string;
  readonly snapshot: boolean;
  readonly expired: boolean;
  readonly preview: readonly Entry[];
}

export type Attachment = ImageAttachment | LogAttachment;

export interface Answer {
  readonly by: string;
  readonly choice?: string;
  readonly seq: number;
}

export interface Question {
  readonly choices: readonly string[];
  readonly open: boolean;
  readonly answer?: Answer;
}

export interface Sender {
  readonly name: string;
  readonly kind: PrincipalKind;
}

export interface Entry {
  readonly seq: number;
  readonly ts: string;
  readonly src_ts?: string;
  readonly stream: string;
  readonly kind: EntryKind;
  readonly severity: number;
  readonly host?: string;
  readonly app?: string;
  readonly text: string;
  readonly meta?: Readonly<Record<string, unknown>>;
  readonly chat?: string;
  readonly sender?: Sender;
  readonly recipient?: string;
  readonly reply_to?: number;
  readonly segments?: readonly Segment[];
  readonly attachments?: readonly Attachment[];
  readonly seen_by?: readonly string[];
  readonly question?: Question;
  readonly event?: boolean;
}

export interface Claim {
  readonly principal: string;
  readonly note?: string;
  readonly expires_at: string;
}

export interface LineState {
  readonly dtr?: boolean;
  readonly rts?: boolean;
  readonly cts?: boolean;
  readonly dcd?: boolean;
}

export interface Stream {
  readonly id: string;
  readonly kind: string;
  readonly transport: string;
  readonly labels: Readonly<Record<string, string>>;
  readonly status: StreamState;
  readonly last_seq: number;
  readonly last_activity?: string;
  readonly rate_per_min: number;
  readonly dropped: number;
  readonly claim?: Claim;
  readonly line_state?: LineState;
  readonly last_line?: string;
}

export interface ChatCount {
  readonly id: string;
  readonly unread: number;
  readonly open_questions: number;
}

export interface Counts {
  readonly open_questions: number;
  readonly unread_direct: number;
  readonly sources_down: number;
  readonly chats: readonly ChatCount[];
}

export interface Token {
  readonly id: number;
  readonly label: string;
  readonly principal: string;
  readonly kind: PrincipalKind;
  readonly scopes: readonly Scope[];
  readonly created_at: string;
  readonly last_used?: string;
}

export type SnippetLanguage = 'shell' | 'toml' | 'json' | 'env';

export interface Snippet {
  readonly label: string;
  readonly language: SnippetLanguage;
  readonly text: string;
}

export interface TokenCreated extends Token {
  readonly token: string;
  readonly snippets: readonly Snippet[];
}

export interface Session {
  readonly id: number;
  readonly label: string;
  readonly principal: string;
  readonly user_agent?: string;
  readonly created_at: string;
  readonly expires_at: string;
  readonly current: boolean;
}

export interface Pairing {
  readonly id: number;
  readonly url: string;
  readonly qr_url: string;
  readonly expires_at: string;
  readonly principal?: string;
  readonly new_name?: string;
}

export interface Page<T> {
  readonly items: readonly T[];
  readonly has_more: boolean;
  readonly next_seq?: number;
  readonly next_before_seq?: number;
  readonly focus_seq?: number;
}

export interface Reset {
  readonly reason: string;
}

export interface ErrorBody {
  readonly code: string;
  readonly message: string;
  readonly retry_after_s?: number;
}

export interface MarkCreated {
  readonly seq: number;
}

// Request bodies (internal/dto/web_c.go).
export interface TokenCreate {
  readonly principal: string;
  readonly kind?: PrincipalKind;
  readonly label: string;
  readonly scopes: readonly Scope[];
}

export interface PairingCreate {
  readonly principal?: string;
  readonly new_name?: string;
  readonly scopes?: readonly Scope[];
}

// Track D: chats (internal/dto/dto.go Chat, Overview, Share; web_d.go).
export type Surface = 'none' | 'markers' | 'errors';

export interface StreamRef {
  readonly id: string;
  readonly status: StreamState;
}

export interface ChatPreview {
  readonly seq: number;
  readonly sender?: string;
  readonly text: string;
  readonly event: boolean;
}

export interface Chat {
  readonly id: string;
  readonly kind: 'channel' | 'direct';
  readonly name: string;
  readonly title?: string;
  readonly topic?: string;
  readonly peer?: Principal;
  readonly patterns: readonly string[];
  readonly streams: readonly StreamRef[];
  readonly surface: Surface;
  readonly followed: boolean;
  readonly pinned: boolean;
  readonly muted: boolean;
  readonly archived: boolean;
  readonly source: 'runtime' | 'config';
  readonly created_by?: string;
  readonly can_manage: boolean;
  readonly unread: number;
  readonly open_questions: number;
  readonly last_activity?: string;
  readonly preview?: ChatPreview;
}

export interface Overview {
  readonly questions: readonly Entry[];
  readonly direct: readonly Chat[];
  readonly recent: readonly Entry[];
}

export interface Share {
  readonly id: string;
  readonly text?: string;
  readonly files: readonly ImageAttachment[];
  readonly expires_at: string;
}

export interface Seen {
  readonly seq: number;
  readonly by: string;
}

export interface Answered {
  readonly seq: number;
  readonly answer: Answer;
}

export interface Acked {
  readonly acked: number;
}

// Request bodies (internal/dto/web_d.go, dto.go).
export interface ChatPrefs {
  readonly pinned?: boolean;
  readonly muted?: boolean;
  readonly followed?: boolean;
}

export interface ChannelCreate {
  readonly name: string;
  readonly title?: string;
  readonly topic?: string;
  readonly patterns?: readonly string[];
  readonly surface?: Surface;
}

export interface ChannelUpdate {
  readonly rename?: string;
  readonly title?: string;
  readonly topic?: string;
  readonly add_patterns?: readonly string[];
  readonly remove_patterns?: readonly string[];
  readonly surface?: Surface;
  readonly archived?: boolean;
}

/** A log attachment as POST /api/messages takes it (the logs field). */
export interface LogRequest {
  readonly stream?: string;
  readonly channel?: string;
  readonly from_seq: number;
  readonly to_seq?: number;
  readonly grep?: string;
  readonly min_severity?: number;
  readonly title?: string;
}
