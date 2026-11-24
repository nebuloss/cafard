import { ApiClient } from '@shared/api';
import { AuditFilter } from './audit-filter';
import type { AuditEntry, AuditFacets, Config, Link, Member, Notice, Project } from './types';

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
  /** The id to read the next page of the audit log before, or 0 at its end. */
  auditNext = 0;
  auditFacets: AuditFacets = {};
  links: Link[] = [];
  /** What needs the admin, read on every load: the bell is on every page. */
  notices: Notice[] = [];
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
    const [m, p, ns] = await Promise.all([
      this.api.get<{ members: Member[] }>('/members'),
      this.api.get<{ projects: Project[] }>('/projects'),
      this.api.get<{ notices: Notice[] }>('/notices'),
    ]);
    this.members = m.members;
    this.projects = p.projects;
    this.notices = ns.notices;
    if (tab === 'audit') await this.loadAudit(AuditFilter.fromSearch(location.search));
    if (tab === 'overview') this.links = (await this.api.get<{ links: Link[] }>('/links')).links;
    if (!this.config) this.config = await this.api.get<Config>('/config');
  }

  async loadNotices(): Promise<void> {
    this.notices = (await this.api.get<{ notices: Notice[] }>('/notices')).notices;
  }

  /** The newest page of the audit log as filter narrows it, and its columns' values. */
  async loadAudit(filter: AuditFilter): Promise<void> {
    const q = filter.query();
    const [page, facets] = await Promise.all([
      this.api.get<{ entries: AuditEntry[]; next: number }>(`/audit${q ? '?' + q : ''}`),
      this.api.get<AuditFacets>('/audit/facets'),
    ]);
    this.audit = page.entries;
    this.auditNext = page.next;
    this.auditFacets = facets;
  }

  /** The next page of the log, after the ones read. */
  async moreAudit(filter: AuditFilter): Promise<void> {
    if (!this.auditNext) return;
    const q = filter.query();
    const page = await this.api.get<{ entries: AuditEntry[]; next: number }>(`/audit?${q ? q + '&' : ''}before=${this.auditNext}`);
    this.audit = [...this.audit, ...page.entries];
    this.auditNext = page.next;
  }

  /** The notices not marked read: what the bell counts. */
  unread(): Notice[] {
    return this.notices.filter((x) => !x.read);
  }

  async markRead(ids: string[]): Promise<void> {
    await this.api.post('/notices/read', { ids });
    const now = new Date().toISOString();
    for (const x of this.notices) if (ids.includes(x.id)) x.read = now;
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
