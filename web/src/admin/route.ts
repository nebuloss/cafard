import type { IconName } from '@shared/icons';

/**
 * The admin UI's page tree, and reading a location against it, as
 * claudication's admin UI does (web/src/ui/route.ts there).
 *
 * Every page has a path of its own, and the path is the tree:
 *
 *   /                              the overview: what to review, links waiting, storage
 *   /members                       people, agents and devices
 *   /members/<name>                one member: everything about it, on one page
 *   /projects                      the projects
 *   /projects/<name>               one project: everything about it, on one page
 *   /audit                         the audit log
 *   /settings                      the configuration
 *
 * So a link, a bookmark and Back all land on a page, never on "whatever was
 * open last". An item has no sub-pages: what there is to see and change on a
 * member or a project fits one page of cards (the owner's call, 2026-11-23).
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

export type ItemKind = 'members' | 'projects';

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

/**
 * The page a path means, as its canonical path: itself when it names a page,
 * completed or moved when it does not. An unknown part falls back to the
 * level above rather than to a blank screen: a URL is something people type,
 * edit and truncate.
 */
export function canonical(pathname: string): string {
  const [top, name] = segments(pathname);
  switch (top) {
    case undefined:
    case 'overview':
      return '/';
    case 'members':
    case 'projects':
      return name === undefined ? pathOf(top) : pathOf(top, name);
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
}

export function placeOf(pathname: string): Place {
  const [top, name] = segments(canonical(pathname));
  const tab = (TABS.some((t) => t.id === top) ? top : 'overview') as Tab;
  return { tab, name };
}

/** A place to go, and what the navigation calls it. */
export interface NavLink {
  href: string;
  label: string;
  icon?: IconName;
}

/**
 * The rail: the sections, and which one the page is in (an item's page is
 * in its list's section). back is for a rail of a deeper level, which there
 * is none of while items have no sub-pages.
 */
export interface NavLevel {
  back?: NavLink;
  items: NavLink[];
  current: string;
}

export function navLevel(pathname: string): NavLevel {
  const p = placeOf(pathname);
  return {
    // The overview is the root itself, reached by the trail's home step and
    // the name in the top bar, so it is not one of the rail's entries (as in
    // claudication); on it, no entry is marked.
    items: TABS.filter((t) => t.id !== 'overview').map((t) => ({ href: pathOf(t.id), label: t.label, icon: t.icon })),
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
    out.push({ href: pathOf(p.tab, p.name), label: name(p.tab, p.name) });
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
