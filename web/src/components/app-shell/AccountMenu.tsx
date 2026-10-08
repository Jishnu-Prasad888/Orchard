/**
 * The account button in the top bar: who is signed in, the appearance, the
 * shortcuts, settings, and sign out — the things a person reaches for about
 * their session rather than their library.
 */

import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { useAuth } from '../../auth/authContext';
import { readStoredTheme, storeTheme, type ThemePreference } from '../../lib/theme';
import { Menu, type MenuEntry, useMenuButton } from '../ui/Menu';

const THEME_LABEL: Record<ThemePreference, string> = {
  system: 'Appearance: System',
  light: 'Appearance: Light',
  dark: 'Appearance: Dark',
};

const NEXT_THEME: Record<ThemePreference, ThemePreference> = {
  system: 'light',
  light: 'dark',
  dark: 'system',
};

export function AccountMenu({ onShowShortcuts }: { onShowShortcuts: () => void }) {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const menu = useMenuButton();
  const [theme, setTheme] = useState<ThemePreference>(readStoredTheme);

  const initial = (user?.username ?? '?').slice(0, 1).toUpperCase();
  const role = user?.role === 'admin' ? 'Administrator' : 'Member';

  const items: MenuEntry[] = [
    {
      id: 'theme',
      label: THEME_LABEL[theme],
      icon: theme === 'dark' ? 'moon' : theme === 'light' ? 'sun' : 'monitor',
      onSelect: () => {
        const next = NEXT_THEME[theme];
        storeTheme(next);
        setTheme(next);
      },
    },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: onShowShortcuts },
    { id: 'settings', label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings') },
    'separator',
    {
      id: 'signout',
      label: 'Sign out',
      icon: 'logout',
      onSelect: () => void logout(),
      testId: 'sign-out',
    },
  ];

  return (
    <>
      <button
        type="button"
        className="account-button"
        aria-haspopup="menu"
        aria-expanded={menu.open}
        aria-label={`Account: ${user?.username ?? ''}`}
        onClick={menu.toggle}
        data-testid="account-button"
      >
        <span className="account-avatar" aria-hidden="true">
          {initial}
        </span>
      </button>
      {menu.anchor && (
        <AccountPanel
          anchor={menu.anchor}
          items={items}
          onClose={menu.close}
          name={user?.username ?? ''}
          role={role}
        />
      )}
    </>
  );
}

function AccountPanel({
  anchor,
  items,
  onClose,
  name,
  role,
}: {
  anchor: NonNullable<ReturnType<typeof useMenuButton>['anchor']>;
  items: MenuEntry[];
  onClose: () => void;
  name: string;
  role: string;
}) {
  // Who is signed in is information, not an action, so it is the menu's
  // header rather than an item to activate.
  return (
    <Menu
      anchor={anchor}
      align="end"
      items={items}
      onClose={onClose}
      label={`Signed in as ${name}`}
      header={
        <div className="account-identity">
          <span className="account-avatar account-avatar-lg" aria-hidden="true">
            {name.slice(0, 1).toUpperCase()}
          </span>
          <span className="account-identity-text">
            <span className="account-identity-name">{name}</span>
            <span className="account-identity-role">{role}</span>
          </span>
        </div>
      }
    />
  );
}
