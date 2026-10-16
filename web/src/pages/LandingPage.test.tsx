import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import LandingPage from './LandingPage';

function renderLanding() {
  return render(
    <MemoryRouter initialEntries={['/landing']}>
      <LandingPage />
    </MemoryRouter>,
  );
}

describe('LandingPage', () => {
  it('leads with the tagline', () => {
    renderLanding();
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('A library just for you.');
  });

  it('explains why Orchard exists and what it promises', () => {
    renderLanding();
    const why = screen.getByRole('region', { name: 'Why Orchard exists' });
    expect(within(why).getByText(/Books end up scattered/)).toBeInTheDocument();
    for (const promise of [
      'Your files stay where they are.',
      'What you mark stays with the book.',
      'It runs on your own computer.',
    ]) {
      expect(within(why).getByText(promise)).toBeInTheDocument();
    }
  });

  it('sends people to set up or sign in, and nowhere that does not exist', () => {
    renderLanding();
    const targets = new Set(
      screen.getAllByRole('link').map((link) => link.getAttribute('href') ?? ''),
    );
    expect(targets).toEqual(new Set(['/landing', '/setup', '/login']));
    expect(screen.getAllByRole('link', { name: 'Set up Orchard' })[0]).toHaveAttribute(
      'href',
      '/setup',
    );
  });
});
