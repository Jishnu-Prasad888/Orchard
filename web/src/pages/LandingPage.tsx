import { useId } from 'react';
import { Link } from 'react-router-dom';

import Brand from '../components/Brand';
import { Icon, type IconName } from '../components/ui/Icon';
import './LandingPage.css';

/**
 * Public marketing page at /landing. Explains what Orchard is: a calm,
 * self-hosted personal library — content first, nothing flashy.
 *
 * Not part of the authenticated app shell: no sidebar, no library context.
 * Every link here goes to a route that already exists (/login, /setup).
 */

interface Feature {
  icon: IconName;
  title: string;
  body: string;
}

const FEATURES: Feature[] = [
  {
    icon: 'folder',
    title: 'Where your files already are',
    body: 'Point Orchard at a directory and it indexes what is there in place. Nothing is copied, moved, or rewritten — your filesystem stays the source of truth.',
  },
  {
    icon: 'tag',
    title: 'Tags and notes',
    body: 'Label anything, write a note on any file, and come back to a library organized the way you actually think about it.',
  },
  {
    icon: 'search',
    title: 'Find anything',
    body: 'Fast full-text search across your whole library, without waiting on anyone else’s index.',
  },
  {
    icon: 'lock',
    title: 'Yours alone',
    body: 'Self-hosted on hardware you control. No subscription, no one else’s server, no one else’s eyes on your library.',
  },
  {
    icon: 'share',
    title: 'Share deliberately',
    body: 'Grant access by account, or hand out a password-protected link — only when and to whom you choose.',
  },
  {
    icon: 'restore',
    title: 'Durable by design',
    body: 'Backups are built in, so what you keep here stays kept.',
  },
];

/**
 * A simple divider between sections. Purely decorative, so it is aria-hidden.
 */
function SectionDivider() {
  const reactId = useId().replace(/:/g, '');
  const gradId = `divider-fade-${reactId}`;
  return (
    <div className="landing-divider" aria-hidden="true">
      <svg viewBox="0 0 1200 24" preserveAspectRatio="none" className="landing-divider-svg">
        <defs>
          <linearGradient id={gradId} x1="0" x2="1" y1="0" y2="0">
            <stop offset="0%" stopColor="var(--color-border)" stopOpacity="0" />
            <stop offset="50%" stopColor="var(--color-border)" stopOpacity="1" />
            <stop offset="100%" stopColor="var(--color-border)" stopOpacity="0" />
          </linearGradient>
        </defs>
        <line x1="0" y1="12" x2="1200" y2="12" stroke={`url(#${gradId})`} strokeWidth="1" />
      </svg>
    </div>
  );
}

export default function LandingPage() {
  return (
    <div className="landing" data-testid="landing-page">
      <header className="landing-nav">
        <div className="landing-nav-inner">
          <Link to="/landing" className="landing-nav-brand">
            <Brand>Orchard</Brand>
          </Link>
          <nav className="landing-nav-links" aria-label="Page sections">
            <a href="#meaning">The name</a>
            <a href="#features">What it does</a>
            <Link to="/login" className="button">
              Sign in
            </Link>
          </nav>
        </div>
      </header>

      <main>
        <section className="landing-hero">
          <div className="landing-hero-copy">
            <h1 className="landing-hero-brand">
              <Brand>Orchard</Brand>
            </h1>
            <p className="landing-hero-tagline">
              A calm, personal library that grows at your own pace.
            </p>
            <p className="landing-hero-sub">
              Your own files, kept in place, on hardware you own, organized the way you actually
              want them.
            </p>
            <div className="landing-hero-actions">
              <Link to="/setup" className="button primary-button landing-cta">
                Get started
              </Link>
              <Link to="/login" className="button landing-cta">
                Sign in
              </Link>
            </div>
          </div>
        </section>

        <SectionDivider />

        <section id="meaning" className="landing-section landing-meaning">
          <p className="landing-eyebrow">The name</p>
          <h2 className="landing-heading">Why &ldquo;Orchard&rdquo;?</h2>
          <div className="landing-meaning-body">
            <p>
              An orchard is planted once and tended for years. Nothing in it is finished on day
              one — it is added to a little at a time, and what you planted last season is still
              there, unbothered, when you come back to it.
            </p>
            <p>
              That is the idea here: a library that is yours to grow slowly, without anyone
              rearranging it behind your back, and without anything you add ever needing to be
              moved, re-encoded, or handed to someone else&rsquo;s server to be useful.
            </p>
          </div>
        </section>

        <SectionDivider />

        <section id="features" className="landing-section">
          <p className="landing-eyebrow">What it does</p>
          <h2 className="landing-heading">A foundation, not a feature list</h2>
          <ul className="landing-feature-grid">
            {FEATURES.map((feature) => (
              <li key={feature.title} className="landing-feature-card">
                <span className="landing-feature-icon">
                  <Icon name={feature.icon} size={22} />
                </span>
                <h3>{feature.title}</h3>
                <p>{feature.body}</p>
              </li>
            ))}
          </ul>
        </section>

        <SectionDivider />

        <section className="landing-section landing-final-cta">
          <h2 className="landing-heading">Start your own orchard</h2>
          <p className="landing-hero-sub">
            A few minutes of setup, and your library has somewhere to live.
          </p>
          <div className="landing-hero-actions">
            <Link to="/setup" className="button primary-button landing-cta">
              Get started
            </Link>
            <Link to="/login" className="button landing-cta">
              Sign in
            </Link>
          </div>
        </section>
      </main>

      <footer className="landing-footer">
        <span>
          <Brand>Orchard</Brand> — self-hosted, and yours.
        </span>
      </footer>
    </div>
  );
}
