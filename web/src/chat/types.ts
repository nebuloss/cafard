// The shapes /api/v1 sends, as internal/httpapi/api writes them.

export type Kind = 'person' | 'agent' | 'device';

export interface MemberRef {
  name: string;
  kind: Kind;
  display_name?: string;
  removed?: boolean;
}

export interface Me extends MemberRef {
  admin?: boolean;
}

export interface Project {
  id: number;
  name: string;
  archived?: boolean;
}

export interface Conversation {
  id: number;
  address: string;
  name: string;
  title: string;
  messages: number;
  unread?: number;
  last_at?: string;
  closed?: boolean;
  last?: { author: string; text?: string; files?: number };
  live?: boolean;
}

export type FileState = 'live' | 'stored' | 'cut' | 'deleted';

export interface FileInfo {
  id: number;
  name: string;
  type: string;
  size: number;
  state: FileState;
  url: string;
  lines_url?: string;
}

export interface Message {
  id: number;
  author: MemberRef;
  /** Its sender deleted it: no text, no files, a line saying so. */
  deleted?: boolean;
  /** Its sender changed its text since it was sent. */
  edited?: boolean;
  text?: string;
  at: string;
  files?: FileInfo[];
}

export interface ReadBy extends MemberRef {
  up_to: number;
  at: string;
}

export interface Member extends MemberRef {
  description?: string;
}

export interface Line {
  n: number;
  text: string;
  cut?: boolean;
}

export interface LinesPage {
  lines: Line[];
  total: number;
  next?: number;
  matches?: number;
}

export interface Signin {
  id: number;
  surface: 'admin' | 'chat';
  what: string;
  from: string;
  last_used: string;
  since: string;
  current?: boolean;
}

/** What /api/v1/events says changed. */
export interface Changed {
  topic: 'project' | 'conversation' | 'me' | 'resync';
  project?: number;
  conversation?: number;
}
