// The shapes /admin/api sends, as internal/httpapi/admin writes them.

export type Kind = 'person' | 'agent' | 'device';
export type LinkState = 'waiting' | 'used' | 'cancelled' | 'expired';

export interface MemberRef {
  name: string;
  kind: Kind;
  display_name?: string;
}

export interface Link {
  id: number;
  /** password: from before people had none; no new one is made. */
  kind: 'person' | 'password' | 'agent';
  member: string;
  state: LinkState;
  created_by: string;
  created_at: string;
  expires_at: string;
  used_at?: string;
  used_from?: string;
  /** Only when the link was just made: its code is not kept. */
  url?: string;
  qr?: string;
}

export interface Member extends MemberRef {
  description?: string;
  enabled: boolean;
  removed?: boolean;
  joined: boolean;
  projects: string[];
  last_used?: string;
  last_from?: string;
  signins?: number;
  link?: Link;
}

export interface Project {
  name: string;
  archived?: boolean;
  retention_days: number;
  quota_bytes: number;
  members: MemberRef[];
  conversations: number;
  files: number;
  bytes: number;
}

export interface AuditEntry {
  id: number;
  at: string;
  actor: string;
  action: string;
  target: string;
  origin: string;
  /** members, projects, links or sign-ins: worked out from the action. */
  kind: string;
  /** admin, chat, cli, join, mcp, api: the origin before its address. */
  source: string;
}

/** A column's values in the audit log, with how many entries have each. */
export type AuditFacets = Partial<Record<'actor' | 'kind' | 'target' | 'source', { value: string; count: number }[]>>;

export interface Signin {
  id: number;
  surface: 'admin' | 'chat';
  what: string;
  from: string;
  last_used: string;
  since: string;
  current?: boolean;
}

export interface Config {
  listen: string;
  admin_listen: string;
  public_url: string;
  state_dir: string;
  max_upload: number;
  version: string;
  link_hours: number;
  link_max_hours: number;
  admin_signin_hours: number;
  chat_signin_days: number;
}

/** Something that needs the admin now (internal/httpapi/admin/notices.go). */
export interface Notice {
  id: string;
  severity: 'error' | 'warn' | 'info';
  kind: 'member' | 'project';
  title: string;
  detail?: string;
  link?: string;
  since?: string;
  read?: string;
}
