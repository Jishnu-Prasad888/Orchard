/**
 * The navigation sidebar: persistent on desktop, a drawer below 900px (opened
 * from "More" in the bottom bar).
 *
 * Browsing comes first and is always expanded. Organizing and upkeep live in a
 * collapsible "Manage" group so they are one click away without competing with
 * the library for attention. Settings sits at the foot.
 */

import { useState } from 'react';
import { Link, NavLink, useLocation } from 'react-router-dom';

import { useMLSwitch } from '../../api/mlSwitch';
import { Icon } from '../ui/Icon';
import { MANAGE_NAV, type NavItem, PRIMARY_NAV, SETTINGS_NAV } from './navigation';

const MANAGE_KEY = 'cairn.nav.manage';

function readManageOpen(): boolean {
  try {
    return localStorage.getItem(MANAGE_KEY) === 'open';
  } catch {
    return false;
  }
}

function NavEntry({ item, onNavigate }: { item: NavItem; onNavigate?: (() => void) | undefined }) {
  return (
    <NavLink
      to={item.to}
      end={item.end ?? false}
      onClick={onNavigate}
      className={({ isActive }) => (isActive ? 'nav-item active' : 'nav-item')}
    >
      <Icon name={item.icon} className="nav-item-icon" />
      <span className="nav-item-label">{item.label}</span>
    </NavLink>
  );
}

export function Sidebar({ open, onNavigate }: { open: boolean; onNavigate: () => void }) {
  const location = useLocation();
  // People only exist while machine learning is on.
  const mlOn = useMLSwitch();
  const primary = PRIMARY_NAV.filter((item) => item.to !== '/people' || mlOn !== false);
  const inManage = MANAGE_NAV.some((item) => location.pathname.startsWith(item.to));
  const [manageWanted, setManageWanted] = useState(readManageOpen);
  // Navigating into a page of the group opens it so the active entry is
  // visible; after that the user can still collapse it.
  const [forcedOpen, setForcedOpen] = useState(inManage);
  const [prevPath, setPrevPath] = useState(location.pathname);
  if (prevPath !== location.pathname) {
    setPrevPath(location.pathname);
    if (inManage) setForcedOpen(true);
  }
  const manageOpen = manageWanted || forcedOpen;

  const toggleManage = () => {
    const next = !manageOpen;
    setManageWanted(next);
    setForcedOpen(next);
    try {
      localStorage.setItem(MANAGE_KEY, next ? 'open' : 'closed');
    } catch {
      // Not remembered.
    }
  };

  return (
    <aside
      id="app-sidebar"
      className={open ? 'sidebar is-open' : 'sidebar'}
      aria-label="Navigation"
      data-testid="app-sidebar"
    >
      <Link to="/" className="sidebar-brand" aria-label="Cairn home" onClick={onNavigate}>
        <span className="cairn-mark" aria-hidden="true">
          <span />
          <span />
          <span />
        </span>
        <span className="brand sidebar-wordmark">Cairn</span>
      </Link>

      <nav className="sidebar-nav" aria-label="Sections">
        {primary.map((item) => (
          <NavEntry key={item.to} item={item} onNavigate={onNavigate} />
        ))}
      </nav>

      <div className="sidebar-group">
        <button
          type="button"
          className="sidebar-group-toggle"
          aria-expanded={manageOpen}
          aria-controls="sidebar-manage"
          onClick={toggleManage}
        >
          <span>Manage</span>
          <Icon name="chevron-down" size={16} className={manageOpen ? 'is-flipped' : undefined} />
        </button>
        <nav id="sidebar-manage" className="sidebar-nav" aria-label="Manage" hidden={!manageOpen}>
          {MANAGE_NAV.map((item) => (
            <NavEntry key={item.to} item={item} onNavigate={onNavigate} />
          ))}
        </nav>
      </div>

      <div className="sidebar-foot">
        <nav className="sidebar-nav" aria-label="Settings">
          <NavEntry item={SETTINGS_NAV} onNavigate={onNavigate} />
        </nav>
      </div>
    </aside>
  );
}
