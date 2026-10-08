import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import ForbiddenPage from './ForbiddenPage';

function setup(state?: { message?: string }, initialEntries: string[] = ['/403']) {
  return render(
    <MemoryRouter initialEntries={initialEntries.map((pathname) => ({ pathname, state }))}>
      <Routes>
        <Route path="/403" element={<ForbiddenPage />} />
        <Route path="/settings" element={<div>settings page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ForbiddenPage', () => {
  it('shows the reason reported by the server', () => {
    setup({ message: 'You are not allowed to list libraries.' });

    expect(screen.getByText('You are not allowed to list libraries.')).toBeInTheDocument();
    expect(screen.getByText('403')).toBeInTheDocument();
  });

  it('explains the problem when no reason was supplied', () => {
    setup();

    expect(screen.getByText(/You do not have permission to view this page/)).toBeInTheDocument();
  });

  it('can go back to the previous page', () => {
    // Two entries so "Go back" (navigate(-1)) lands on the previous route.
    setup(undefined, ['/settings', '/403']);

    fireEvent.click(screen.getByRole('button', { name: 'Go back' }));
    expect(screen.getByText('settings page')).toBeInTheDocument();
  });

  it('links to account settings', () => {
    setup({ message: 'nope' });

    fireEvent.click(screen.getByRole('link', { name: 'Account settings' }));
    expect(screen.getByText('settings page')).toBeInTheDocument();
  });
});
