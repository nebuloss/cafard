import { ApiClient } from '@shared/api';
import type { AuditEntry, Config, Link, Member, MemberRef, Project } from './types';

/**
 * AdminStore holds what the admin UI shows, read from /admin/api. Every
 * change is sent to the API and the store read again from it, so what is on
 * screen is what the server holds rather than what the page guessed.
 */
export class AdminStore {
  me: MemberRef | null = null;
  members: Member[] = [];
  projects: Project[] = [];
  audit: AuditEntry[] = [];
  links: Link[] = [];
  config: Config | null = null;

  constructor(readonly api: ApiClient) {}

  async signIn(username: string, password: string): Promise<void> {
    const r = await this.api.post<{ member: MemberRef }>('/signin', { username, password });
    this.me = r.member;
  }

  async signOut(): Promise<void> {
    await this.api.post('/signout');
    this.me = null;
  }

  /** Who is signed in, or null; never throws for a signed-out page. */
  async whoAmI(): Promise<MemberRef | null> {
    try {
      this.me = (await this.api.get<{ member: MemberRef }>('/me')).member;
    } catch {
      this.me = null;
    }
    return this.me;
  }

  /** Reads what every view needs, and what the view named needs besides. */
  async load(view: string): Promise<void> {
    const [m, p] = await Promise.all([
      this.api.get<{ members: Member[] }>('/members'),
      this.api.get<{ projects: Project[] }>('/projects'),
    ]);
    this.members = m.members;
    this.projects = p.projects;
    if (view === 'audit') this.audit = (await this.api.get<{ entries: AuditEntry[] }>('/audit')).entries;
    if (view === 'storage') this.links = (await this.api.get<{ links: Link[] }>('/links')).links;
    if (!this.config) this.config = await this.api.get<Config>('/config');
  }

  member(name: string): Member | undefined {
    return this.members.find((m) => m.name === name);
  }

  project(name: string): Project | undefined {
    return this.projects.find((p) => p.name === name);
  }

  static path(name: string): string {
    return encodeURIComponent(name);
  }
}
