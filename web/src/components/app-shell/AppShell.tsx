/**
 * The application frame around every signed-in page: the sidebar (a drawer
 * on narrow screens), the top bar with search, the bottom bar on phones, and
 * the things that float above pages — the upload tray and the shortcuts sheet.
 *
 * The page itself is the scroll container's only content, so a long photo grid
 * scrolls the window and the bars stay put.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';

import { useLibraries } from '../../api/libraries';
import { useFfmpegAvailable } from '../../api/ffmpeg';
import { notifyLibraryChanged } from '../../api/libraryEvents';
import { useToast } from '../ui/Toast';
import { FaceModelHost } from './FaceModelHost';
import { UploadTray } from '../upload/UploadTray';
import { MobileNav } from './MobileNav';
import { ShortcutsDialog } from './ShortcutsDialog';
import { Sidebar } from './Sidebar';
import { TopBar } from './TopBar';
import { useIndexStatus } from './useIndexStatus';
import './AppShell.css';

function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  return !!el && (/^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) || el.isContentEditable);
}

export default function AppShell() {
  const location = useLocation();
  const navigate = useNavigate();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const moreRef = useRef<HTMLButtonElement | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);
  const { libraryId } = useLibraries();

  // Once per sign-in (per tab session): without ffmpeg many videos get no
  // thumbnail. The toast closes itself after 7 seconds or when dismissed.
  const toast = useToast();
  const ffmpeg = useFfmpegAvailable();
  useEffect(() => {
    if (ffmpeg !== false) return;
    try {
      if (sessionStorage.getItem('cairn.ffmpegWarned')) return;
      sessionStorage.setItem('cairn.ffmpegWarned', '1');
    } catch {
      // Storage unavailable: warn on every load rather than never.
    }
    toast({
      message:
        'ffmpeg is not installed on the server, so some video thumbnails will not appear. Install ffmpeg and restart Cairn.',
      tone: 'error',
      duration: 7000,
    });
  }, [ffmpeg, toast]);

  // The server rescans the library on its own; this notices when that found
  // something (administrators get the index counts)...
  useIndexStatus(libraryId);
  // ...and everyone else gets a refresh when they come back to the tab after
  // a while away, which is when files added elsewhere are most likely.
  useEffect(() => {
    if (!libraryId) return;
    let hiddenAt = 0;
    const onVisibility = () => {
      if (document.hidden) {
        hiddenAt = Date.now();
      } else if (hiddenAt && Date.now() - hiddenAt > 20000) {
        notifyLibraryChanged(libraryId);
      }
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, [libraryId]);

  // Navigating closes the drawer. Adjusted during render rather than in an
  // effect, so the drawer is never drawn open over the new page.
  const [prevPath, setPrevPath] = useState(location.pathname);
  if (prevPath !== location.pathname) {
    setPrevPath(location.pathname);
    setDrawerOpen(false);
  }

  const closeDrawer = useCallback(() => setDrawerOpen(false), []);

  // Escape closes the drawer and returns focus to the button that opened it.
  useEffect(() => {
    if (!drawerOpen) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      setDrawerOpen(false);
      moreRef.current?.focus();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [drawerOpen]);

  // Global shortcuts: "/" to search, "?" for the shortcut sheet. Ignored while
  // typing, and while a dialog or the viewer owns the keyboard.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isTyping(event.target)) return;
      if (document.querySelector('[aria-modal="true"]')) return;
      if (event.key === '/') {
        event.preventDefault();
        const input = searchRef.current;
        // On phones the top bar hides its search box; Search is a tab instead.
        const hidden = input
          ? getComputedStyle(input.closest('.topbar-search') ?? input).display === 'none'
          : true;
        if (input && !hidden) {
          input.focus();
        } else {
          navigate('/search');
        }
      } else if (event.key === '?') {
        event.preventDefault();
        setShortcutsOpen(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [navigate]);

  return (
    <div className="app-shell" data-testid="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>

      {drawerOpen && (
        <div className="app-drawer-backdrop" onClick={closeDrawer} data-testid="nav-backdrop" />
      )}

      <Sidebar open={drawerOpen} onNavigate={closeDrawer} />

      <div className="app-main">
        <TopBar searchRef={searchRef} onShowShortcuts={() => setShortcutsOpen(true)} />
        <div className="app-content" id="main-content" tabIndex={-1}>
          <Outlet />
        </div>
      </div>

      <MobileNav
        moreOpen={drawerOpen}
        onMore={() => setDrawerOpen((open) => !open)}
        moreRef={moreRef}
      />

      <UploadTray />
      <FaceModelHost />
      <ShortcutsDialog open={shortcutsOpen} onClose={() => setShortcutsOpen(false)} />
    </div>
  );
}
