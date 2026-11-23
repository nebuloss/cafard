import { ApiClient } from '@shared/api';
import type { AuditEntry, Config, Link, Member, Project } from './types';

/**
 * AdminStore holds what the admin UI shows, read from /admin/api. Every
 * change is sent to the API and the store read again from it, so what is on
 * screen is what the server holds rather than what the page guessed.
 */
export class AdminStore {
  /** Whether a sign-in is open. */
  signedIn = false;
  members: Member[] = [];
  projects: Project[] = [];
  audit: AuditEntry[] = [];
  links: Link[] = [];
  config: Config | null = null;

  constructor(readonly api: ApiClient) {}

  /** Signs in with the admin password, the only one that opens this UI. */
  async signIn(password: string): Promise<void> {
    await this.api.post('/signin', { password });
    this.signedIn = true;
  }

  async signOut(): Promise<void> {
    await this.api.post('/signout');
    this.signedIn = false;
  }

  /** Whether a sign-in is open; never throws for a signed-out page. */
  async whoAmI(): Promise<boolean> {
    try {
      await this.api.get('/me');
      this.signedIn = true;
    } catch {
      this.signedIn = false;
    }
    return this.signedIn;
  }

  /** Reads what every page needs, and what the tab's pages need besides. */
  async load(tab: string): Promise<void> {
    const [m, p] = await Promise.all([
      this.api.get<{ members: Member[] }>('/members'),
      this.api.get<{ projects: Project[] }>('/projects'),
    ]);
    this.members = m.members;
    this.projects = p.projects;
    if (tab === 'audit') this.audit = (await this.api.get<{ entries: AuditEntry[] }>('/audit')).entries;
    if (tab === 'overview') this.links = (await this.api.get<{ links: Link[] }>('/links')).links;
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
