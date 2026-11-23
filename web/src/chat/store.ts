import { ApiClient } from '@shared/api';
import type { Conversation, Me, Member, Message, Project, ReadBy } from './types';

/** A conversation's address split in two: be98/wifi-drops. */
export class Address {
  constructor(
    readonly project: string,
    readonly name: string,
  ) {}

  static parse(address: string): Address | null {
    const i = address.indexOf('/');
    if (i <= 0 || i === address.length - 1) return null;
    return new Address(address.slice(0, i), address.slice(i + 1));
  }

  toString(): string {
    return `${this.project}/${this.name}`;
  }

  /** The address in an /api/v1 path, each part encoded. */
  get path(): string {
    return `${encodeURIComponent(this.project)}/${encodeURIComponent(this.name)}`;
  }
}

/**
 * Thread is one open conversation's messages as far as they are loaded:
 * the latest page when it opened, new ones as they arrive, earlier ones as
 * the reader scrolls back.
 */
export class Thread {
  messages: Message[] = [];
  readBy: ReadBy[] = [];
  conversation: Conversation | null = null;
  hasEarlier = false;

  constructor(
    readonly address: Address,
    private readonly api: ApiClient,
  ) {}

  private get path(): string {
    return `/projects/${this.address.path.replace('/', '/conversations/')}/messages`;
  }

  /** Loads the latest page. */
  async open(): Promise<void> {
    const r = await this.api.get<MessagesResponse>(this.path);
    this.messages = r.messages;
    this.readBy = r.read_by;
    this.conversation = r.conversation;
    this.hasEarlier = r.messages.length > 0 && (r.messages[0]?.id ?? 0) > 0 && r.conversation.messages > r.messages.length;
  }

  /** Loads what arrived since the last message, and who has read what. */
  async refresh(): Promise<boolean> {
    const last = this.messages[this.messages.length - 1]?.id ?? 0;
    const r = await this.api.get<MessagesResponse>(`${this.path}?after=${last}`);
    this.readBy = r.read_by;
    this.conversation = r.conversation;
    // A message already shown may have changed (a live file grew, a file was
    // deleted): read the last page again to catch it, merged by id.
    const tail = await this.api.get<MessagesResponse>(`${this.path}?before=${last + 1}&limit=20`);
    const byId = new Map(this.messages.map((m) => [m.id, m]));
    for (const m of [...tail.messages, ...r.messages]) byId.set(m.id, m);
    const before = this.messages.length;
    this.messages = [...byId.values()].sort((a, b) => a.id - b.id);
    return this.messages.length > before;
  }

  /** Loads the page before the first loaded message. */
  async earlier(): Promise<void> {
    const first = this.messages[0]?.id;
    if (!first) return;
    const r = await this.api.get<MessagesResponse>(`${this.path}?before=${first}`);
    this.messages = [...r.messages, ...this.messages];
    this.hasEarlier = r.messages.length > 0;
  }
}

interface MessagesResponse {
  conversation: Conversation;
  messages: Message[];
  read_by: ReadBy[];
}

/** What sending returns: where it went, and the message. */
export interface Sent {
  address: string;
  message?: Message;
  conversation?: Conversation;
}

/**
 * Sender turns "this text and these files" into the calls /api/v1 takes:
 * the text first (or a new conversation started by it), then each file
 * added to that same message, so a caption and its photos stay one
 * message.
 */
export class Sender {
  constructor(private readonly api: ApiClient) {}

  async send(project: string, conversation: string | null, text: string, files: File[]): Promise<Address> {
    text = text.trim();
    let address: Address;
    let messageId = 0;
    if (!conversation) {
      const title = text ? undefined : files.length === 1 ? files[0]?.name : `${files.length} files`;
      const r = await this.api.post<Sent>(`/projects/${encodeURIComponent(project)}/conversations`, { text, ...(title ? { title } : {}) });
      address = Address.parse(r.address) as Address;
      messageId = r.message?.id ?? 0;
    } else {
      address = new Address(project, conversation);
      if (text) {
        const r = await this.api.send<Sent>('POST', `/send/${address.path}`, text, 'text/plain; charset=utf-8');
        messageId = r.message?.id ?? 0;
      }
    }
    for (const f of files) {
      const path = `/send/${address.path}/${encodeURIComponent(f.name || 'pasted.png')}${messageId ? `?message=${messageId}` : ''}`;
      const r = await this.api.send<Sent>('PUT', path, f, f.type || undefined);
      if (!messageId) messageId = r.message?.id ?? 0;
    }
    return address;
  }
}

/**
 * ChatStore holds what the chat app shows, read from /api/v1, and reads it
 * again when the event stream says it changed.
 */
export class ChatStore {
  me: Me | null = null;
  projects: Project[] = [];
  readonly conversations = new Map<string, Conversation[]>();
  readonly members = new Map<string, Member[]>();
  readonly threads = new Map<string, Thread>();
  readonly sender: Sender;

  constructor(readonly api: ApiClient) {
    this.sender = new Sender(api);
  }

  async whoAmI(): Promise<Me | null> {
    try {
      this.me = (await this.api.get<{ member: Me }>('/me')).member;
    } catch {
      this.me = null;
    }
    return this.me;
  }

  async signOut(): Promise<void> {
    await this.api.post('/signout');
    this.me = null;
  }

  /** Reads the projects and every project's conversations. */
  async loadAll(): Promise<void> {
    this.projects = (await this.api.get<{ projects: Project[] }>('/projects')).projects;
    await Promise.all(this.projects.map((p) => this.loadConversations(p.name)));
    for (const name of [...this.conversations.keys()]) {
      if (!this.projects.some((p) => p.name === name)) this.conversations.delete(name);
    }
  }

  async loadConversations(project: string): Promise<void> {
    const r = await this.api.get<{ conversations: Conversation[] }>(`/projects/${encodeURIComponent(project)}/conversations`);
    this.conversations.set(project, r.conversations);
  }

  async loadMembers(project: string): Promise<Member[]> {
    const r = await this.api.get<{ members: Member[] }>(`/projects/${encodeURIComponent(project)}/members`);
    this.members.set(project, r.members);
    return r.members;
  }

  projectById(id: number): Project | undefined {
    return this.projects.find((p) => p.id === id);
  }

  conversation(address: Address): Conversation | undefined {
    return this.conversations.get(address.project)?.find((c) => c.name === address.name);
  }

  /** The thread of a conversation, opened if it was not. */
  async thread(address: Address): Promise<Thread> {
    const key = address.toString();
    let t = this.threads.get(key);
    if (!t) {
      t = new Thread(address, this.api);
      await t.open();
      this.threads.set(key, t);
    }
    return t;
  }

  /** The open thread of a conversation id, if one is open. */
  threadById(projectId: number, conversationId: number): Thread | undefined {
    const p = this.projectById(projectId);
    if (!p) return undefined;
    for (const t of this.threads.values()) {
      if (t.address.project === p.name && t.conversation?.id === conversationId) return t;
    }
    return undefined;
  }
}
