/**
 * Where everything lives, in the order a person meets it: their media first,
 * then what they have organized, then what they have made, then the
 * housekeeping. The sidebar, the mobile bottom bar, and the "More" sheet all
 * read from here, so the three never disagree.
 */

import type { IconName } from '../ui/Icon';

export interface NavItem {
  to: string;
  label: string;
  icon: IconName;
  /** Match the route exactly — used for "/" so Home is not always active. */
  end?: boolean;
}

/** The library: what people open Cairn for. */
export const PRIMARY_NAV: NavItem[] = [
  { to: '/', label: 'Home', icon: 'home', end: true },
  { to: '/photos', label: 'Photos', icon: 'photo' },
  { to: '/videos', label: 'Videos', icon: 'video' },
  { to: '/albums', label: 'Albums', icon: 'album' },
  { to: '/people', label: 'People', icon: 'people' },
  { to: '/memories', label: 'Memories', icon: 'memory' },
  { to: '/files', label: 'Files', icon: 'folder' },
  { to: '/favorites', label: 'Favorites', icon: 'star' },
  { to: '/shared', label: 'Shared', icon: 'share' },
  { to: '/trash', label: 'Trash', icon: 'trash' },
];

/**
 * Organizing and upkeep. Secondary, collapsed by default, and kept out of the
 * way of browsing: most visits never need them.
 */
export const MANAGE_NAV: NavItem[] = [
  { to: '/tags', label: 'Tags', icon: 'tag' },
  { to: '/duplicates', label: 'Duplicates', icon: 'copy' },
  { to: '/similar', label: 'Similar photos', icon: 'camera' },
  { to: '/libraries', label: 'Libraries', icon: 'drive' },
  { to: '/permissions', label: 'Permissions', icon: 'lock' },
  { to: '/ml', label: 'Machine learning', icon: 'spark' },
  { to: '/backups', label: 'Backups', icon: 'archive' },
];

export const SETTINGS_NAV: NavItem = { to: '/settings', label: 'Settings', icon: 'settings' };

/** The four destinations a phone shows in its bottom bar; "More" holds the rest. */
export const MOBILE_NAV: NavItem[] = [
  { to: '/', label: 'Home', icon: 'home', end: true },
  { to: '/photos', label: 'Photos', icon: 'photo' },
  { to: '/albums', label: 'Albums', icon: 'album' },
  { to: '/settings', label: 'Settings', icon: 'settings' },
];
