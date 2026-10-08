import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { admin, member, mockApi, noContent, originalFetch, renderPage } from '../../test/harness';
import Providers from '../../test/Providers';
import AppShell from './AppShell';

/** The shell renders an `Outlet`, so the routes have to be declared to render. */
function shell(route = '/') {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <Providers>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<p>home content</p>} />
            <Route path="/photos" element={<p>photos content</p>} />
            <Route path="/settings" element={<p>settings content</p>} />
          </Route>
        </Routes>
      </Providers>
    </MemoryRouter>,
  );
}

describe('AppShell', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('lists every browsing section, in the order a person meets them', () => {
    mockApi();
    renderPage(<AppShell />);

    const sections = screen.getByRole('navigation', { name: 'Sections' });
    const labels = within(sections)
      .getAllByRole('link')
      .map((link) => link.textContent);
    expect(labels).toEqual([
      'Home',
      'Photos',
      'Videos',
      'Albums',
      'People',
      'Memories',
      'Files',
      'Favorites',
      'Shared',
      'Trash',
    ]);
    // Settings sits apart, at the foot.
    expect(
      within(screen.getByRole('navigation', { name: 'Settings' })).getByRole('link', {
        name: 'Settings',
      }),
    ).toBeInTheDocument();
  });

  it('keeps organizing and upkeep in a collapsible Manage group', () => {
    mockApi();
    renderPage(<AppShell />);

    const toggle = screen.getByRole('button', { name: 'Manage' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');

    const manage = screen.getByRole('navigation', { name: 'Manage' });
    expect(
      within(manage)
        .getAllByRole('link')
        .map((link) => link.textContent),
    ).toEqual([
      'Tags',
      'Duplicates',
      'Similar photos',
      'Libraries',
      'Permissions',
      'Machine learning',
      'Backups',
    ]);
  });

  it('marks the current section', async () => {
    mockApi();
    shell('/photos');

    const nav = screen.getByRole('navigation', { name: 'Sections' });
    expect(within(nav).getByRole('link', { name: 'Photos' })).toHaveClass('active');
    expect(within(nav).getByRole('link', { name: 'Home' })).not.toHaveClass('active');
  });

  it('opens and closes the narrow-screen drawer', async () => {
    mockApi();
    shell();

    const toggle = screen.getByTestId('nav-toggle');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByTestId('nav-backdrop')).not.toBeInTheDocument();

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    // The backdrop is what makes the drawer dismissable on a phone.
    expect(screen.getByTestId('nav-backdrop')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('nav-backdrop'));
    await waitFor(() => {
      expect(screen.getByTestId('nav-toggle')).toHaveAttribute('aria-expanded', 'false');
    });
  });

  it('returns focus to the menu button when Escape closes the drawer', async () => {
    mockApi();
    shell();

    const toggle = screen.getByTestId('nav-toggle');
    toggle.focus();
    fireEvent.click(toggle);

    fireEvent.keyDown(document, { key: 'Escape' });

    await waitFor(() => {
      expect(screen.getByTestId('nav-toggle')).toHaveAttribute('aria-expanded', 'false');
    });
    expect(toggle).toHaveFocus();
  });

  it('sends the settings search to the matching settings section', async () => {
    mockApi();
    shell();

    fireEvent.change(screen.getByLabelText('Search settings'), { target: { value: 'theme' } });
    fireEvent.submit(screen.getByRole('search'));

    expect(await screen.findByText('settings content')).toBeInTheDocument();
  });

  it('focuses the settings search with the slash key', () => {
    mockApi();
    shell();

    fireEvent.keyDown(window, { key: '/' });
    expect(screen.getByLabelText('Search settings')).toHaveFocus();
  });

  it('shows who is signed in and lets them sign out', async () => {
    const fetchMock = mockApi(
      [
        (url, init) =>
          init?.method === 'POST' && url.endsWith('/api/v1/auth/logout') ? noContent() : undefined,
      ],
      { user: member },
    );
    shell();

    // The account is read from the session, so it arrives a tick after render.
    fireEvent.click(await screen.findByRole('button', { name: 'Account: alice' }));
    const menu = await screen.findByRole('menu');
    expect(within(menu).getByText('alice')).toBeInTheDocument();
    expect(within(menu).getByText('Member')).toBeInTheDocument();

    fireEvent.click(within(menu).getByTestId('sign-out'));
    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(([input]) => String(input).includes('/api/v1/auth/logout')),
      ).toBe(true);
    });
  });

  it('labels an administrator as such', async () => {
    mockApi([], { user: admin });
    shell();

    fireEvent.click(await screen.findByRole('button', { name: 'Account: jishnu' }));
    expect(await screen.findByText('Administrator')).toBeInTheDocument();
  });

  it('opens the shortcut sheet with the question mark', async () => {
    mockApi();
    shell();

    fireEvent.keyDown(window, { key: '?' });
    expect(await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })).toBeInTheDocument();
  });

  it('switches the theme from the account menu', async () => {
    mockApi();
    shell();

    fireEvent.click(await screen.findByRole('button', { name: /Account:/ }));
    fireEvent.click(await screen.findByRole('menuitem', { name: /Appearance: System/ }));
    expect(document.documentElement.dataset.theme).toBe('light');
    document.documentElement.removeAttribute('data-theme');
  });
});
