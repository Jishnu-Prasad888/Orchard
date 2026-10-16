import '@fontsource-variable/literata/opsz.css';
import '@fontsource-variable/literata/opsz-italic.css';
import { Link } from 'react-router-dom';

import Brand from '../components/Brand';
import './LandingPage.css';

/**
 * Public page at /landing: what Orchard is, and why it exists.
 *
 * The page is built around one image from the subject itself — a page of a
 * book someone has been reading in Orchard, with a passage highlighted, a
 * bookmark ribbon, and a note in the margin. Everything else stays quiet:
 * a short essay on why Orchard was made, three promises it keeps, what it
 * does, and where your reading data actually lives.
 *
 * Outside the app shell (no sidebar, no library context). Links go only to
 * routes that exist: /setup and /login. Reading type is Literata, loaded
 * with this page alone (it's a lazy route), so the app never pays for it.
 */

interface Group {
  title: string;
  lines: string[];
}

const WHAT_IT_DOES: Group[] = [
  {
    title: 'Read',
    lines: [
      'PDF, EPUB, Markdown and plain text, in one reader.',
      'Turn pages or scroll, one page or two side by side.',
      'Light, dark or sepia, at the size and spacing you like.',
    ],
  },
  {
    title: 'Mark',
    lines: [
      'Highlight in five colours, with a note if you want one.',
      'Save quotes with your own thoughts beside them.',
      'Bookmark a page and give it a name.',
    ],
  },
  {
    title: 'Find',
    lines: [
      'Search the words inside your books, not just their file names.',
      'Search what you’ve highlighted and quoted, too.',
      'Gather books with tags, collections and favourites.',
    ],
  },
  {
    title: 'Keep',
    lines: [
      'Back up every library, then verify or restore it.',
      'Share one book by link, or a library by account.',
      'Add family or friends; each person has their own shelf.',
    ],
  },
];

function MarkedPage() {
  return (
    <figure className="landing-page-figure" aria-label="A page of Walden, marked up in Orchard">
      <div className="landing-sheet">
        <span className="landing-ribbon" aria-hidden="true" />
        <p className="landing-sheet-head">
          <span>Walden</span>
          <span>Chapter 2</span>
        </p>
        <p className="landing-sheet-text">
          <mark className="landing-highlight">
            I went to the woods because I wished to live deliberately,
          </mark>{' '}
          to front only the essential facts of life, and see if I could not learn what it had to
          teach, and not, when I came to die, discover that I had not lived. I did not wish to live
          what was not life, living is so dear.
        </p>
        <p className="landing-margin-note">Read this again in spring.</p>
      </div>
      <figcaption className="landing-figcaption">
        Henry David Thoreau, <cite>Walden</cite> (1854), as it looks in Orchard’s reader.
      </figcaption>
    </figure>
  );
}

export default function LandingPage() {
  return (
    <div className="landing" data-testid="landing-page">
      <header className="landing-nav">
        <Link to="/landing" className="landing-nav-brand">
          <Brand>Orchard</Brand>
        </Link>
        <Link to="/login" className="button ghost-button">
          Sign in
        </Link>
      </header>

      <main>
        <section className="landing-hero" aria-labelledby="landing-title">
          <div className="landing-hero-copy">
            <h1 id="landing-title" className="landing-title">
              A library <span className="landing-title-phrase">just for you.</span>
            </h1>
            <p className="landing-lede">
              Orchard gathers the books you already have into one quiet place, on a computer you
              own. It remembers where you stopped, and keeps everything you mark along the way.
            </p>
            <div className="landing-actions">
              <Link to="/setup" className="button primary-button landing-cta">
                Set up Orchard
              </Link>
              <Link to="/login" className="button landing-cta">
                Sign in
              </Link>
            </div>
          </div>
          <MarkedPage />
        </section>

        <section className="landing-why" aria-labelledby="landing-why-title">
          <h2 id="landing-why-title" className="landing-heading">
            Why Orchard exists
          </h2>
          <div className="landing-essay">
            <p>
              Books end up scattered. Some are in a downloads folder, some on an old laptop, some on
              a drive in a drawer, and the rest inside three different reading apps. Each app keeps
              its own copy, its own idea of where you stopped, and your highlights inside its own
              account. Change apps, or lose the account, and the margins of every book you have read
              go with it.
            </p>
            <p>
              Orchard was made to put all of that back in one place that belongs to you. It reads
              your files where they already are, and keeps what you mark in them in the library
              itself, not on someone else’s server. No store, no recommendations, nobody else’s
              catalogue between you and your books.
            </p>
          </div>

          <dl className="landing-promises">
            <div>
              <dt>Your files stay where they are.</dt>
              <dd>
                Orchard reads your folders as they are. Nothing is copied, renamed or converted, and
                you can still open every file without it.
              </dd>
            </div>
            <div>
              <dt>What you mark stays with the book.</dt>
              <dd>
                Your place, bookmarks, highlights and quotes are saved inside the library folder, so
                they move wherever it goes.
              </dd>
            </div>
            <div>
              <dt>It runs on your own computer.</dt>
              <dd>
                No subscription and no account with anyone else. Your reading is nobody’s business
                but yours.
              </dd>
            </div>
          </dl>
        </section>

        <section className="landing-does" aria-labelledby="landing-does-title">
          <h2 id="landing-does-title" className="landing-heading">
            What it does
          </h2>
          <div className="landing-groups">
            {WHAT_IT_DOES.map((group) => (
              <div key={group.title} className="landing-group">
                <h3>{group.title}</h3>
                <ul>
                  {group.lines.map((line) => (
                    <li key={line}>{line}</li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </section>

        <section className="landing-portable" aria-labelledby="landing-portable-title">
          <div className="landing-portable-copy">
            <h2 id="landing-portable-title" className="landing-heading">
              Take it with you
            </h2>
            <p>
              Everything Orchard knows about your reading lives in one small folder next to your
              books. Unplug the drive, connect it to another computer running Orchard, and sign in
              with the same name: your place in every book is still there, and so is every highlight
              and quote.
            </p>
          </div>
          <pre
            className="landing-tree"
            aria-label="A library folder: your books, plus a .orchard folder holding your reading data"
          >
            {`Books/
├── Walden.epub
├── Meditations.pdf
├── Field Notes.md
└── .orchard/
    └── library.db   `}
            <span className="landing-tree-note">your place, highlights, quotes</span>
          </pre>
        </section>

        <section className="landing-closing" aria-labelledby="landing-closing-title">
          <h2 id="landing-closing-title" className="landing-heading">
            Start your library
          </h2>
          <p>Set it up in a few minutes, on a computer you already have.</p>
          <div className="landing-actions">
            <Link to="/setup" className="button primary-button landing-cta">
              Set up Orchard
            </Link>
            <Link to="/login" className="button landing-cta">
              Sign in
            </Link>
          </div>
        </section>
      </main>

      <footer className="landing-footer">
        <Brand>Orchard</Brand>
        <span>A library just for you, kept on your own computer.</span>
      </footer>
    </div>
  );
}
