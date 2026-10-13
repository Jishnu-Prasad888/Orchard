/**
 * Where everything lives, in the order a person meets it: their library
 * first, then what they have organized, then the housekeeping. The sidebar,
 * the mobile bottom bar, and the "More" sheet all read from here, so the
 * three never disagree.
 */

import type { IconName } from '../ui/Icon';

export interface NavItem {
  to: string;
  label: string;
  icon: IconName;
  /** Match the route exactly — used for "/" so Home is not always active. */
  end?: boolean;
}

/** The library: what people open Orchard for. */
export const PRIMARY_NAV: NavItem[] = [
  { to: '/', label: 'Home', icon: 'home', end: true },
  { to: '/files', label: 'Files', icon: 'folder' },
  { to: '/favorites', label: 'Favorites', icon: 'star' },
  { to: '/bookmarks', label: 'Bookmarks', icon: 'bookmark' },
  { to: '/quotes', label: 'Quotes', icon: 'quote' },
  { to: '/tags', label: 'Tags', icon: 'tag' },
  { to: '/collections', label: 'Collections', icon: 'list' },
  { to: '/shared', label: 'Shared', icon: 'share' },
  { to: '/trash', label: 'Trash', icon: 'trash' },
];

/**
 * Administration. Secondary, collapsed by default, and kept out of the way
 * of browsing: most visits never need them.
 */
export const MANAGE_NAV: NavItem[] = [
  { to: '/libraries', label: 'Libraries', icon: 'drive' },
  { to: '/permissions', label: 'Permissions', icon: 'lock' },
  { to: '/backups', label: 'Backups', icon: 'archive' },
];

export const SETTINGS_NAV: NavItem = { to: '/settings', label: 'Settings', icon: 'settings' };

/** The four destinations a phone shows in its bottom bar; "More" holds the rest. */
export const MOBILE_NAV: NavItem[] = [
  { to: '/', label: 'Home', icon: 'home', end: true },
  { to: '/files', label: 'Files', icon: 'folder' },
  { to: '/search', label: 'Search', icon: 'search' },
  { to: '/settings', label: 'Settings', icon: 'settings' },
];
