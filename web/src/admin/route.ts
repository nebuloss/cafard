import type { IconName } from '@shared/icons';

/**
 * The admin UI's page tree, and reading a location against it, as
 * claudication's admin UI does (web/src/ui/route.ts there).
 *
 * Every page has a path of its own, and the path is the tree:
 *
 *   /                              the overview: what to review, links waiting, storage
 *   /members                       people, agents and devices
 *   /members/<name>                one member: who it is, its link, its description
 *   /members/<name>/<page>         its projects, its sign-ins or token, pausing and removing
 *   /projects                      the projects
 *   /projects/<name>               one project: what it holds
 *   /projects/<name>/<page>        its members, its files, archiving and deleting
 *   /audit                         the audit log
 *   /settings                      the configuration
 *
 * So a link, a bookmark and Back all land on a page, never on "whatever was
 * open last". Each level's overview is the level itself (an item's own path)
 * rather than a page named Overview under it.
 *
 * Pure: string in, string out, no window, so it is tested under node.
 */

export interface Section {
  id: string;
  label: string;
  icon?: IconName;
}

/** The top-level screens, in the order the rail lists them. */
export const TABS = [
  { id: 'overview', label: 'Overview', icon: 'home' },
  { id: 'members', label: 'Members', icon: 'people' },
  { id: 'projects', label: 'Projects', icon: 'folder' },
  { id: 'audit', label: 'Audit log', icon: 'list' },
  { id: 'settings', label: 'Settings', icon: 'gear' },
] as const satisfies readonly Section[];
export type Tab = (typeof TABS)[number]['id'];

/** A member's pages under /members/<name>, its overview being that path itself. */
export const MEMBER_PAGES = [
  { id: 'projects', label: 'Projects', icon: 'folder' },
  { id: 'access', label: 'Access', icon: 'key' },
  { id: 'advanced', label: 'Advanced', icon: 'warn' },
] as const satisfies readonly Section[];

/** A project's pages under /projects/<name>. */
export const PROJECT_PAGES = [
  { id: 'members', label: 'Members', icon: 'people' },
  { id: 'files', label: 'Files', icon: 'disk' },
  { id: 'advanced', label: 'Advanced', icon: 'warn' },
] as const satisfies readonly Section[];

export type ItemKind = 'members' | 'projects';
export type MemberPage = (typeof MEMBER_PAGES)[number]['id'] | '';
export type ProjectPage = (typeof PROJECT_PAGES)[number]['id'] | '';

export function pagesOf(kind: ItemKind): readonly Section[] {
  return kind === 'members' ? MEMBER_PAGES : PROJECT_PAGES;
}

/** The path's segments, decoded. A malformed escape is kept as it came. */
export function segments(pathname: string): string[] {
  return pathname
    .split('/')
    .filter((s) => s !== '')
    .map((s) => {
      try {
        return decodeURIComponent(s);
      } catch {
        return s;
      }
    });
}

/** A path from segments, each encoded. */
export function pathOf(...parts: string[]): string {
  return '/' + parts.map(encodeURIComponent).join('/');
}

const known = (list: readonly Section[], id: string | undefined): id is string =>
  id !== undefined && list.some((s) => s.id === id);

/**
 * The page a path means, as its canonical path: itself when it names a page,
 * completed or moved when it does not. An unknown part falls back to the
 * level above rather than to a blank screen: a URL is something people type,
 * edit and truncate.
 */
export function canonical(pathname: string): string {
  const [top, name, page] = segments(pathname);
  switch (top) {
    case undefined:
    case 'overview':
      return '/';
    case 'members':
    case 'projects':
      if (name === undefined) return pathOf(top);
      return known(pagesOf(top), page) ? pathOf(top, name, page) : pathOf(top, name);
    case 'audit':
    case 'settings':
      return pathOf(top);
  }
  return '/';
}

/** Where a path is: its tab, the item it names, and the item's page. */
export interface Place {
  tab: Tab;
  name?: string;
  page: string;
}

export function placeOf(pathname: string): Place {
  const [top, name, page] = segments(canonical(pathname));
  const tab = (TABS.some((t) => t.id === top) ? top : 'overview') as Tab;
  return { tab, name, page: page ?? '' };
}

/** A place to go, and what the navigation calls it. */
export interface NavLink {
  href: string;
  label: string;
  icon?: IconName;
}

/**
 * The rail for one page: the choices on its own level, which one is this
 * page, and the way up a level. Only the last level is listed, so the rail
 * stays a handful of entries however deep the page is; the trail shows every
 * level at once.
 */
export interface NavLevel {
  back?: NavLink;
  items: NavLink[];
  current: string;
}

export function navLevel(pathname: string): NavLevel {
  const p = placeOf(pathname);
  if ((p.tab === 'members' || p.tab === 'projects') && p.name !== undefined) {
    const base = pathOf(p.tab, p.name);
    const tab = TABS.find((t) => t.id === p.tab)!;
    return {
      back: { href: pathOf(p.tab), label: tab.label, icon: tab.icon },
      items: [
        { href: base, label: p.name, icon: p.tab === 'members' ? 'person' : 'folder' },
        ...pagesOf(p.tab).map((s) => ({ href: `${base}/${s.id}`, label: s.label, icon: s.icon })),
      ],
      current: p.page ? `${base}/${p.page}` : base,
    };
  }
  return {
    items: TABS.map((t) => ({ href: t.id === 'overview' ? '/' : pathOf(t.id), label: t.label, icon: t.icon })),
    current: p.tab === 'overview' ? '/' : pathOf(p.tab),
  };
}

/** Names an item for the trail: a member's display name, a project's name. */
export type Namer = (kind: ItemKind, name: string) => string;

/** Every level from the root down to this page, the last one being it. */
export function trailOf(pathname: string, name: Namer): NavLink[] {
  const p = placeOf(pathname);
  const out: NavLink[] = [{ href: '/', label: 'Overview', icon: 'home' }];
  if (p.tab === 'overview') return out;
  const tab = TABS.find((t) => t.id === p.tab)!;
  out.push({ href: pathOf(p.tab), label: tab.label, icon: tab.icon });
  if ((p.tab === 'members' || p.tab === 'projects') && p.name !== undefined) {
    const base = pathOf(p.tab, p.name);
    out.push({ href: base, label: name(p.tab, p.name) });
    const page = pagesOf(p.tab).find((s) => s.id === p.page);
    if (page) out.push({ href: `${base}/${page.id}`, label: page.label });
  }
  return out;
}

/** The path a pre-router link meant: #members was the members page. */
export function fromHash(hash: string): string | null {
  const h = hash.replace(/^#/, '');
  if (h === '') return null;
  if (h === 'storage') return '/';
  if (h === 'config') return '/settings';
  return TABS.some((t) => t.id === h) ? canonical('/' + h) : null;
}
