/**
 * Cairn's one icon set.
 *
 * Every glyph is drawn on the same 24px grid with the same 1.75 stroke and
 * round joins, so icons from different parts of the app sit at the same visual
 * weight. Icons are always decorative (`aria-hidden`); the control that holds
 * one carries the accessible name.
 */

import type { ReactNode } from 'react';

const ICONS = {
  home: (
    <>
      <path d="M4 10.2 12 4l8 6.2V19a1 1 0 0 1-1 1h-4.5v-5.5h-5V20H5a1 1 0 0 1-1-1z" />
    </>
  ),
  photo: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2.5" />
      <circle cx="9" cy="10" r="1.6" />
      <path d="m20.5 15.5-4.6-4.6a1 1 0 0 0-1.4 0L6 19.5" />
    </>
  ),
  video: (
    <>
      <rect x="3" y="6" width="13" height="12" rx="2.5" />
      <path d="m16 10.5 4.4-2.6a.4.4 0 0 1 .6.35v7.5a.4.4 0 0 1-.6.35L16 13.5" />
    </>
  ),
  album: (
    <>
      <rect x="4" y="4" width="12.5" height="12.5" rx="2.2" />
      <path d="M7.5 20h10a2.5 2.5 0 0 0 2.5-2.5v-10" />
      <path d="m16.5 13-3.2-3.2a1 1 0 0 0-1.4 0L6.5 15.2" />
    </>
  ),
  people: (
    <>
      <circle cx="9.5" cy="8.5" r="3.5" />
      <path d="M3.5 19.5c.6-3.1 3-5 6-5s5.4 1.9 6 5" />
      <path d="M16 5.2a3.4 3.4 0 0 1 0 6.6M17.8 14.6c1.5.7 2.5 2.4 2.7 4.9" />
    </>
  ),
  person: (
    <>
      <circle cx="12" cy="8.5" r="3.8" />
      <path d="M5 20c.7-3.6 3.5-5.6 7-5.6s6.3 2 7 5.6" />
    </>
  ),
  memory: (
    <>
      <path d="M6 3.5h10.5a2 2 0 0 1 2 2v13a2 2 0 0 1-2 2H6a1 1 0 0 1-1-1v-15a1 1 0 0 1 1-1z" />
      <path d="M9 3.5v17" />
      <path d="M12 8.5h3.5M12 12h3.5" />
    </>
  ),
  folder: (
    <>
      <path d="M3.5 7.5a2 2 0 0 1 2-2h3.6l2 2.2h7.4a2 2 0 0 1 2 2v7.8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z" />
    </>
  ),
  'folder-move': (
    <>
      <path d="M3.5 7.5a2 2 0 0 1 2-2h3.6l2 2.2h7.4a2 2 0 0 1 2 2v7.8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z" />
      <path d="M9 13.5h6M12.8 11.2l2.3 2.3-2.3 2.3" />
    </>
  ),
  file: (
    <>
      <path d="M14 3.5H7.5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h9a2 2 0 0 0 2-2V8z" />
      <path d="M14 3.5V8h4.5" />
    </>
  ),
  document: (
    <>
      <path d="M14 3.5H7.5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h9a2 2 0 0 0 2-2V8z" />
      <path d="M14 3.5V8h4.5M9 12.5h6M9 16h4" />
    </>
  ),
  audio: (
    <>
      <path d="M9 18V6.5l10-2V16" />
      <circle cx="6.5" cy="18" r="2.5" />
      <circle cx="16.5" cy="16" r="2.5" />
    </>
  ),
  star: <path d="m12 4 2.4 4.9 5.4.8-3.9 3.8.9 5.4L12 16.4l-4.8 2.5.9-5.4-3.9-3.8 5.4-.8z" />,
  share: (
    <>
      <circle cx="17.5" cy="6" r="2.5" />
      <circle cx="6.5" cy="12" r="2.5" />
      <circle cx="17.5" cy="18" r="2.5" />
      <path d="m8.7 10.8 6.6-3.6M8.7 13.2l6.6 3.6" />
    </>
  ),
  link: (
    <>
      <path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1" />
      <path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1" />
    </>
  ),
  trash: (
    <>
      <path d="M4.5 7h15M9.5 7V5a1 1 0 0 1 1-1h3a1 1 0 0 1 1 1v2" />
      <path d="m6.5 7 .8 11.6a1.5 1.5 0 0 0 1.5 1.4h6.4a1.5 1.5 0 0 0 1.5-1.4L17.5 7" />
      <path d="M10.2 11v5M13.8 11v5" />
    </>
  ),
  restore: (
    <>
      <path d="M4.5 12a7.5 7.5 0 1 0 2.2-5.3L4.5 9" />
      <path d="M4.5 4.5V9H9" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19.2 14.6a1.4 1.4 0 0 0 .3 1.5l.1.1a1.7 1.7 0 1 1-2.4 2.4l-.1-.1a1.4 1.4 0 0 0-1.5-.3 1.4 1.4 0 0 0-.9 1.3v.2a1.7 1.7 0 0 1-3.4 0v-.1a1.4 1.4 0 0 0-.9-1.3 1.4 1.4 0 0 0-1.5.3l-.1.1a1.7 1.7 0 1 1-2.4-2.4l.1-.1a1.4 1.4 0 0 0 .3-1.5 1.4 1.4 0 0 0-1.3-.9h-.2a1.7 1.7 0 0 1 0-3.4h.1a1.4 1.4 0 0 0 1.3-.9 1.4 1.4 0 0 0-.3-1.5l-.1-.1a1.7 1.7 0 1 1 2.4-2.4l.1.1a1.4 1.4 0 0 0 1.5.3h.1a1.4 1.4 0 0 0 .8-1.3v-.2a1.7 1.7 0 0 1 3.4 0v.1a1.4 1.4 0 0 0 .8 1.3 1.4 1.4 0 0 0 1.5-.3l.1-.1a1.7 1.7 0 1 1 2.4 2.4l-.1.1a1.4 1.4 0 0 0-.3 1.5v.1a1.4 1.4 0 0 0 1.3.8h.2a1.7 1.7 0 0 1 0 3.4h-.1a1.4 1.4 0 0 0-1.3.8z" />
    </>
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m20 20-4.4-4.4" />
    </>
  ),
  close: <path d="M6.5 6.5l11 11M17.5 6.5l-11 11" />,
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  'chevron-left': <path d="m14.5 5.5-6.5 6.5 6.5 6.5" />,
  'chevron-right': <path d="m9.5 5.5 6.5 6.5-6.5 6.5" />,
  'chevron-down': <path d="m6 9.5 6 6 6-6" />,
  'arrow-left': <path d="M19 12H5.5M11 5.5 4.5 12l6.5 6.5" />,
  plus: <path d="M12 5v14M5 12h14" />,
  minus: <path d="M5 12h14" />,
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  more: (
    <>
      <circle cx="12" cy="5.5" r="1.2" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1.2" fill="currentColor" stroke="none" />
      <circle cx="12" cy="18.5" r="1.2" fill="currentColor" stroke="none" />
    </>
  ),
  download: (
    <>
      <path d="M12 4v11M7.5 10.5 12 15l4.5-4.5" />
      <path d="M5 19.5h14" />
    </>
  ),
  upload: (
    <>
      <path d="M12 15.5v-11M7.5 9 12 4.5 16.5 9" />
      <path d="M5 19.5h14" />
    </>
  ),
  info: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 11v5.5M12 7.8v.1" />
    </>
  ),
  alert: (
    <>
      <path d="M10.3 4.6 3.2 17a2 2 0 0 0 1.7 3h14.2a2 2 0 0 0 1.7-3L13.7 4.6a2 2 0 0 0-3.4 0z" />
      <path d="M12 9.5v4M12 16.8v.1" />
    </>
  ),
  fullscreen: <path d="M4.5 9V4.5H9M19.5 9V4.5H15M4.5 15v4.5H9M19.5 15v4.5H15" />,
  play: <path d="M8 5.6v12.8a.6.6 0 0 0 .9.5l10-6.4a.6.6 0 0 0 0-1L8.9 5.1a.6.6 0 0 0-.9.5z" />,
  pause: <path d="M8.5 5.5v13M15.5 5.5v13" />,
  slideshow: (
    <>
      <rect x="3.5" y="5" width="17" height="12" rx="2" />
      <path d="m10.5 8.8 4 2.2-4 2.2zM8 20.5h8" />
    </>
  ),
  'eye-off': (
    <>
      <path d="M3.5 3.5l17 17" />
      <path d="M10.6 10.6a2 2 0 0 0 2.8 2.8" />
      <path d="M9.9 5.2A9.6 9.6 0 0 1 12 5c5 0 8.5 4.5 9.5 7a13 13 0 0 1-2.6 3.8M6.5 6.6A13 13 0 0 0 2.5 12c1 2.5 4.5 7 9.5 7 1.6 0 3-.4 4.3-1.1" />
    </>
  ),
  tag: (
    <>
      <path d="M3.5 4.5v6l9.3 9.3a1.6 1.6 0 0 0 2.3 0l4.7-4.7a1.6 1.6 0 0 0 0-2.3L10.5 3.5h-6a1 1 0 0 0-1 1z" />
      <circle cx="8" cy="8" r="1.3" />
    </>
  ),
  lock: (
    <>
      <rect x="5" y="10.5" width="14" height="10" rx="2" />
      <path d="M8 10.5V8a4 4 0 0 1 8 0v2.5" />
    </>
  ),
  drive: (
    <>
      <rect x="3.5" y="13" width="17" height="6.5" rx="2" />
      <path d="M5.5 13 8 5.8a1.5 1.5 0 0 1 1.4-1h5.2a1.5 1.5 0 0 1 1.4 1L18.5 13" />
      <path d="M16.5 16.3h.1" />
    </>
  ),
  'drive-off': (
    <>
      <rect x="3.5" y="13" width="17" height="6.5" rx="2" />
      <path d="M5.5 13 8 5.8a1.5 1.5 0 0 1 1.4-1h5.2a1.5 1.5 0 0 1 1.4 1L18.5 13" />
      <path d="M3 3l18 18" />
    </>
  ),
  spark: (
    <>
      <path d="M12 3.5 13.7 9l5.8 1.5-5.8 1.6L12 17.5l-1.7-5.4-5.8-1.6L10.3 9z" />
      <path d="M18.5 15.5l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7z" />
    </>
  ),
  archive: (
    <>
      <rect x="3.5" y="4.5" width="17" height="4.5" rx="1.4" />
      <path d="M5 9v9.5A1.5 1.5 0 0 0 6.5 20h11a1.5 1.5 0 0 0 1.5-1.5V9M10 13h4" />
    </>
  ),
  copy: (
    <>
      <rect x="8.5" y="8.5" width="11.5" height="11.5" rx="2" />
      <path d="M5.5 15.5H5a1.5 1.5 0 0 1-1.5-1.5V5A1.5 1.5 0 0 1 5 3.5h9A1.5 1.5 0 0 1 15.5 5v.5" />
    </>
  ),
  logout: (
    <>
      <path d="M14.5 4.5h3a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2h-3" />
      <path d="M10 8l-4 4 4 4M6 12h9" />
    </>
  ),
  grid: (
    <>
      <rect x="4" y="4" width="6.5" height="6.5" rx="1.5" />
      <rect x="13.5" y="4" width="6.5" height="6.5" rx="1.5" />
      <rect x="4" y="13.5" width="6.5" height="6.5" rx="1.5" />
      <rect x="13.5" y="13.5" width="6.5" height="6.5" rx="1.5" />
    </>
  ),
  list: <path d="M9 6.5h11M9 12h11M9 17.5h11M4.5 6.5h.1M4.5 12h.1M4.5 17.5h.1" />,
  filter: <path d="M4 6h16M7 12h10M10 18h4" />,
  sort: <path d="M7 4.5v15M3.5 16 7 19.5 10.5 16M17 19.5v-15M13.5 8 17 4.5 20.5 8" />,
  sun: (
    <>
      <circle cx="12" cy="12" r="3.8" />
      <path d="M12 2.8v1.7M12 19.5v1.7M2.8 12h1.7M19.5 12h1.7M5.5 5.5l1.2 1.2M17.3 17.3l1.2 1.2M5.5 18.5l1.2-1.2M17.3 6.7l1.2-1.2" />
    </>
  ),
  moon: <path d="M19.5 14.2A7.8 7.8 0 1 1 9.8 4.5a6.2 6.2 0 0 0 9.7 9.7z" />,
  monitor: (
    <>
      <rect x="3" y="4.5" width="18" height="12" rx="2" />
      <path d="M9 20h6M12 16.5V20" />
    </>
  ),
  keyboard: (
    <>
      <rect x="2.5" y="6" width="19" height="12" rx="2" />
      <path d="M6 10h.1M9.3 10h.1M12.6 10h.1M15.9 10h.1M18 10h.1M7.5 14h9" />
    </>
  ),
  edit: (
    <>
      <path d="M15.5 4.8a2 2 0 0 1 2.8 0l.9.9a2 2 0 0 1 0 2.8L9 18.7l-4.5 1 1-4.5z" />
      <path d="m13.5 6.8 3.7 3.7" />
    </>
  ),
  refresh: (
    <>
      <path d="M19.5 12a7.5 7.5 0 0 1-13.1 5" />
      <path d="M4.5 12a7.5 7.5 0 0 1 13.1-5" />
      <path d="M17.6 3.5V7h-3.5M6.4 20.5V17h3.5" />
    </>
  ),
  calendar: (
    <>
      <rect x="4" y="5.5" width="16" height="14.5" rx="2" />
      <path d="M4 10h16M8.5 3.5v4M15.5 3.5v4" />
    </>
  ),
  'image-off': (
    <>
      <path d="M3.5 3.5l17 17" />
      <path d="M20.5 16.5v-10a2 2 0 0 0-2-2h-10M4.6 5.3A2 2 0 0 0 3.5 7v10.5a2 2 0 0 0 2 2h12.8" />
      <path d="m3.5 17 5-5 3 3" />
    </>
  ),
  external: (
    <>
      <path d="M14 4.5h5.5V10M19.5 4.5l-8 8" />
      <path d="M17.5 14v4a2 2 0 0 1-2 2h-9a2 2 0 0 1-2-2V9a2 2 0 0 1 2-2h4" />
    </>
  ),
  'select-all': (
    <>
      <rect x="4" y="4" width="16" height="16" rx="3" />
      <path d="m8.5 12 2.5 2.5 4.5-5" />
    </>
  ),
  pin: (
    <>
      <path d="M12 21s-6.5-5.6-6.5-11a6.5 6.5 0 0 1 13 0c0 5.4-6.5 11-6.5 11z" />
      <circle cx="12" cy="10" r="2.3" />
    </>
  ),
  camera: (
    <>
      <path d="M4.5 8.5a2 2 0 0 1 2-2h1.8l1.5-2h4.4l1.5 2h1.8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2z" />
      <circle cx="12" cy="13" r="3.4" />
    </>
  ),
  ruler: (
    <>
      <rect x="3.5" y="7.5" width="17" height="9" rx="1.5" />
      <path d="M7.5 7.5v3M11 7.5v4.5M14.5 7.5v3M18 7.5v4.5" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </>
  ),
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof ICONS;

export interface IconProps {
  name: IconName;
  /** Rendered size in px. Defaults to 20, the standard control icon. */
  size?: number;
  /** Fill the shape with the current color (a set favorite star, say). */
  filled?: boolean;
  className?: string | undefined;
}

export function Icon({ name, size = 20, filled = false, className }: IconProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill={filled ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      className={className}
    >
      {ICONS[name]}
    </svg>
  );
}
