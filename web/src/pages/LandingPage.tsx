import { type CSSProperties, useId, useState } from 'react';
import { Link } from 'react-router-dom';

import Brand from '../components/Brand';
import { Icon, type IconName } from '../components/ui/Icon';
import './LandingPage.css';

/**
 * Public marketing page at /landing. Explains what Cairn is and, in some
 * depth, where the name comes from — a trail cairn is the closest real-world
 * object to what the app tries to be: something built up quietly, in place,
 * that helps you find your way back.
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
    body: 'Point Cairn at years of photos and video and it indexes them in place. Nothing is copied, moved, or re-encoded — your filesystem stays the source of truth.',
  },
  {
    icon: 'memory',
    title: 'Memories & albums',
    body: 'Turn scattered photos into albums, tags, and memories worth reopening, instead of a folder you never scroll back through.',
  },
  {
    icon: 'people',
    title: 'People, quietly',
    body: 'Optional, fully local face recognition groups people across your library. Nothing leaves your server to make that happen.',
  },
  {
    icon: 'search',
    title: 'Find anything',
    body: 'Fast search across your whole library, by date, place, person, or tag — without waiting on anyone else’s index.',
  },
  {
    icon: 'lock',
    title: 'Yours alone',
    body: 'Self-hosted on hardware you control. No subscription, no one else’s server, no one else’s eyes on your library.',
  },
  {
    icon: 'restore',
    title: 'Durable by design',
    body: 'Backups and duplicate detection are built in, so nothing you quietly kept quietly disappears.',
  },
];

interface Stone {
  cx: number;
  cy: number;
  rx: number;
  ry: number;
  fill: string;
  rotate: number;
}

/** Bottom to top: largest and steadiest first, like a real stack. */
const STONES: Stone[] = [
  { cx: 110, cy: 195, rx: 52, ry: 26, fill: 'var(--color-primary)', rotate: -2 },
  { cx: 117, cy: 157, rx: 41, ry: 21, fill: 'var(--color-accent)', rotate: 3 },
  { cx: 103, cy: 123, rx: 32, ry: 17, fill: 'var(--color-yellow)', rotate: -4 },
  { cx: 112, cy: 93, rx: 23, ry: 13, fill: 'var(--color-blue)', rotate: 2 },
  { cx: 105, cy: 69, rx: 14, ry: 9, fill: 'var(--color-pink)', rotate: -3 },
];

/**
 * A small stack of stones, the page's one recurring motif. In the hero it is
 * `interactive`: each stone lifts on hover and gives a little settling wobble
 * when clicked, like nudging a real one — purely a tactile flourish, so it
 * stays `aria-hidden` rather than pretending to be a control with a purpose.
 */
function CairnMark({
  className,
  interactive = false,
}: {
  className?: string;
  interactive?: boolean;
}) {
  const [wobbling, setWobbling] = useState<number | null>(null);

  return (
    <svg viewBox="0 0 220 240" className={className} aria-hidden="true" focusable="false">
      <ellipse cx="110" cy="211" rx="68" ry="9" fill="var(--color-border)" opacity="0.5" />
      <circle cx="148" cy="58" r="44" fill="var(--color-accent-soft)" opacity="0.55" />
      {STONES.map((stone, i) => (
        <ellipse
          key={i}
          cx={stone.cx}
          cy={stone.cy}
          rx={stone.rx}
          ry={stone.ry}
          fill={stone.fill}
          className={`landing-stone${interactive ? ' landing-stone-interactive' : ''}${
            wobbling === i ? ' is-wobbling' : ''
          }`}
          style={{ '--stone-rotate': `${stone.rotate}deg` } as CSSProperties}
          onClick={interactive ? () => setWobbling(i) : undefined}
          onAnimationEnd={() => setWobbling((current) => (current === i ? null : current))}
        />
      ))}
    </svg>
  );
}

/**
 * A wavy ribbon of "water" run between sections, with a couple of pebbles
 * drifting along its current. Purely decorative scenery for a page about a
 * trail marker, so it is `aria-hidden`.
 */
function RiverDivider() {
  const reactId = useId().replace(/:/g, '');
  const lineId = `river-line-${reactId}`;
  const lineD = 'M0,45 C150,75 300,15 450,42 C600,70 750,12 900,40 C1050,65 1150,25 1200,42';

  return (
    <div className="landing-river" aria-hidden="true">
      <svg viewBox="0 0 1200 90" preserveAspectRatio="none" className="landing-river-svg">
        <path
          className="landing-river-band"
          d="M0,55 C150,20 300,85 450,50 C600,18 750,82 900,48 C1050,20 1150,70 1200,45 L1200,90 L0,90 Z"
        />
        <path id={lineId} className="landing-river-line" d={lineD} fill="none" />
        <circle
          className="landing-pebble landing-pebble-a"
          r="4"
          style={{ offsetPath: `url(#${lineId})` } as CSSProperties}
        />
        <circle
          className="landing-pebble landing-pebble-b"
          r="3"
          style={{ offsetPath: `url(#${lineId})` } as CSSProperties}
        />
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
            <Brand>Cairn</Brand>
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
              <Brand>Cairn</Brand>
            </h1>
            <p className="landing-hero-tagline">
              A small, beautiful, personal digital place that quietly organizes everything you want
              to keep.
            </p>
            <p className="landing-hero-sub">
              Your photos, files, and memories — kept in place, on hardware you own, at whatever
              pace suits you.
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
          <div className="landing-hero-art-wrap">
            <CairnMark className="landing-hero-art" interactive />
            <p className="landing-hero-hint">Give the stones a nudge.</p>
          </div>
        </section>

        <RiverDivider />

        <section id="meaning" className="landing-section landing-meaning">
          <p className="landing-eyebrow">The name</p>
          <h2 className="landing-heading">Why &ldquo;Cairn&rdquo;?</h2>
          <div className="landing-meaning-body">
            <p>
              A cairn is a small stack of stones, built by hand, with no mortar and no blueprint —
              just one stone balanced on the last. People have raised them for thousands of years,
              on moors, mountain passes, and empty coastlines, to mark a way through places that
              have no other sign.
            </p>
            <p>
              A cairn doesn&rsquo;t announce itself. It isn&rsquo;t a monument to anything in
              particular, and no single stone matters. It exists for one quiet reason: so that
              someone passing this way later — maybe you, on the way back — can look up, recognize
              the shape, and know they&rsquo;re still on the path.
            </p>
            <p>
              That&rsquo;s what this is for. Every photo, file, and memory you keep here is one more
              stone set down — nothing dramatic, nothing that needs to be moved or rebuilt
              elsewhere. It just stays standing, in place, so you can always find your way back to
              it.
            </p>
          </div>
          <blockquote className="landing-quote">
            Cairn catalogs and manages existing media in place. It never copies, moves,
            recompresses, or rewrites your original files.
          </blockquote>
        </section>

        <RiverDivider />

        <section id="features" className="landing-section">
          <p className="landing-eyebrow">What it does</p>
          <h2 className="landing-heading">Everything in one quiet place</h2>
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

        <RiverDivider />

        <section className="landing-section landing-final-cta">
          <CairnMark className="landing-final-art" />
          <h2 className="landing-heading">Start your own cairn</h2>
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
          <Brand>Cairn</Brand> — self-hosted, and yours.
        </span>
        <a href="https://github.com/Jishnu-Prasad888/Cairn" target="_blank" rel="noreferrer">
          Source on GitHub
        </a>
      </footer>
    </div>
  );
}
