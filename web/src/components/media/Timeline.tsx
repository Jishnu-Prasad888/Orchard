/**
 * The timeline: a thin ruler fixed to the foot of the window, marking where in
 * time the files on screen fall and letting a click or a drag jump the grid
 * straight there — the way an old contact-sheet index would, a tick per month
 * and a numeral at each year.
 *
 * It reads the grid it controls from the DOM (`[data-testid="file-grid"]`)
 * rather than through props, the same way `MediaGrid` itself already reaches
 * for the sticky top bar — that keeps this component usable from any page
 * without threading refs through three layers of props. Buckets are built
 * from whatever files are already loaded, so there is no extra request on
 * Photos or an album; Files supplies its own recursive listing (see
 * `MediaPage`) because the grid it shows is deliberately just one folder.
 */

import {
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';

import type { FileSummary } from '../../api/types';
import './Timeline.css';

interface Bucket {
  key: string;
  year: string;
  label: string;
  /** Index of this bucket's first file in the array the buckets were built from. */
  startIndex: number;
}

/** `YYYY-MM` in local time, or `null` for an unparseable date. */
function monthOf(iso: string): { key: string; year: string; date: Date } | null {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return null;
  const year = String(date.getFullYear());
  const month = String(date.getMonth() + 1).padStart(2, '0');
  return { key: `${year}-${month}`, year, date };
}

/**
 * One bucket per distinct month, in the order each first appears — not sorted
 * by date. A list already newest-first lays out newest-to-oldest, which is
 * what should line up with the grid above; a hand-ordered album stays in that
 * order too, rather than being silently re-sorted underneath it.
 */
function bucketsOf(files: readonly FileSummary[]): Bucket[] {
  const seen = new Set<string>();
  const buckets: Bucket[] = [];
  files.forEach((file, index) => {
    const month = monthOf(file.mod_time);
    if (!month || seen.has(month.key)) return;
    seen.add(month.key);
    buckets.push({
      key: month.key,
      year: month.year,
      label: month.date.toLocaleDateString(undefined, { month: 'short', year: 'numeric' }),
      startIndex: index,
    });
  });
  return buckets;
}

function clamp(n: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, n));
}

/** The bucket a given file index falls into: the last one starting at or before it. */
function bucketAt(buckets: readonly Bucket[], index: number): Bucket | undefined {
  let found: Bucket | undefined;
  for (const bucket of buckets) {
    if (bucket.startIndex > index) break;
    found = bucket;
  }
  return found;
}

const GRID_SELECTOR = '[data-testid="file-grid"]';

function gridRect(): DOMRect | null {
  return document.querySelector(GRID_SELECTOR)?.getBoundingClientRect() ?? null;
}

export interface TimelineProps {
  files: readonly FileSummary[];
  /** What the timeline is a timeline of, for its accessible name. */
  label: string;
}

export function Timeline({ files, label }: TimelineProps) {
  const trackRef = useRef<HTMLDivElement | null>(null);
  const frame = useRef(0);
  const [scrubbing, setScrubbing] = useState(false);
  const [hoverFraction, setHoverFraction] = useState<number | null>(null);
  const [activeIndex, setActiveIndex] = useState(0);

  const buckets = useMemo(() => bucketsOf(files), [files]);
  const total = files.length;
  const lastIndex = Math.max(total - 1, 1);

  // Keep the playhead in step with manual scrolling, folded through one
  // animation frame so a fast scroll does not flood this with renders.
  useEffect(() => {
    if (total === 0) return;
    const onScroll = () => {
      if (frame.current) return;
      frame.current = requestAnimationFrame(() => {
        frame.current = 0;
        const rect = gridRect();
        if (!rect) return;
        const top = rect.top + window.scrollY;
        const fraction = clamp((window.scrollY - top) / Math.max(rect.height, 1), 0, 1);
        setActiveIndex(Math.round(fraction * lastIndex));
      });
    };
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onScroll);
    return () => {
      window.removeEventListener('scroll', onScroll);
      window.removeEventListener('resize', onScroll);
      if (frame.current) cancelAnimationFrame(frame.current);
    };
  }, [total, lastIndex]);

  const jumpToFraction = useCallback((fraction: number, smooth: boolean) => {
    const rect = gridRect();
    if (!rect) return;
    const top = rect.top + window.scrollY;
    window.scrollTo({
      top: top + clamp(fraction, 0, 1) * rect.height,
      behavior: smooth ? 'smooth' : 'auto',
    });
  }, []);

  const fractionAtClientX = (clientX: number): number => {
    const rect = trackRef.current?.getBoundingClientRect();
    if (!rect) return 0;
    return clamp((clientX - rect.left) / Math.max(rect.width, 1), 0, 1);
  };

  const jumpToIndex = useCallback(
    (index: number) => {
      const clamped = clamp(index, 0, lastIndex);
      setActiveIndex(clamped);
      jumpToFraction(clamped / lastIndex, true);
    },
    [jumpToFraction, lastIndex],
  );

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (total === 0) return;
    event.currentTarget.setPointerCapture?.(event.pointerId);
    setScrubbing(true);
    const fraction = fractionAtClientX(event.clientX);
    setHoverFraction(fraction);
    setActiveIndex(Math.round(fraction * lastIndex));
    jumpToFraction(fraction, false);
  };

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (scrubbing) {
      const fraction = fractionAtClientX(event.clientX);
      setHoverFraction(fraction);
      setActiveIndex(Math.round(fraction * lastIndex));
      jumpToFraction(fraction, false);
    } else if (event.pointerType === 'mouse') {
      setHoverFraction(fractionAtClientX(event.clientX));
    }
  };

  const endScrub = () => {
    setScrubbing(false);
    setHoverFraction(null);
  };

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const step = Math.max(1, Math.round(total / 50));
    if (event.key === 'ArrowRight' || event.key === 'ArrowUp') {
      event.preventDefault();
      jumpToIndex(activeIndex + step);
    } else if (event.key === 'ArrowLeft' || event.key === 'ArrowDown') {
      event.preventDefault();
      jumpToIndex(activeIndex - step);
    } else if (event.key === 'Home') {
      event.preventDefault();
      jumpToIndex(0);
    } else if (event.key === 'End') {
      event.preventDefault();
      jumpToIndex(lastIndex);
    }
  };

  // Nothing loaded yet: no visible feedback until there is something to show.
  if (total === 0) return null;

  // A ruler needs at least two points in time to mark. Say so rather than
  // silently showing nothing — turning the timeline on should always do
  // something visible, even when there isn't enough of a date spread yet.
  if (buckets.length < 2) {
    return (
      <div className="timeline" data-testid="timeline">
        <div className="timeline-track timeline-track-empty" role="status">
          <span className="timeline-empty-label">
            {buckets.length === 1
              ? `Everything loaded so far is from ${buckets[0]!.label}.`
              : "The loaded files don't have usable dates."}
          </span>
        </div>
      </div>
    );
  }

  const previewIndex = hoverFraction !== null ? Math.round(hoverFraction * lastIndex) : activeIndex;
  const current = bucketAt(buckets, previewIndex) ?? buckets[0]!;
  const flagFraction = hoverFraction ?? activeIndex / lastIndex;
  const showLabel = scrubbing || hoverFraction !== null;

  return (
    <div className="timeline" data-testid="timeline">
      <div
        ref={trackRef}
        className={scrubbing ? 'timeline-track is-scrubbing' : 'timeline-track'}
        role="slider"
        tabIndex={0}
        aria-label={`${label} timeline`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(flagFraction * 100)}
        aria-valuetext={current.label}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endScrub}
        onPointerCancel={endScrub}
        onPointerLeave={() => !scrubbing && setHoverFraction(null)}
        onKeyDown={onKeyDown}
      >
        {buckets.map((bucket, i) => (
          <span
            key={bucket.key}
            className={
              bucket.year !== buckets[i - 1]?.year ? 'timeline-tick is-year' : 'timeline-tick'
            }
            style={{ left: `${(bucket.startIndex / lastIndex) * 100}%` }}
            aria-hidden="true"
          >
            {bucket.year !== buckets[i - 1]?.year && (
              <span className="timeline-year">{bucket.year}</span>
            )}
          </span>
        ))}
        <span
          className="timeline-flag"
          style={{ left: `${flagFraction * 100}%` }}
          aria-hidden="true"
        />
      </div>
      {showLabel && (
        <span
          className="timeline-label"
          style={{ left: `${flagFraction * 100}%` }}
          aria-hidden="true"
        >
          {current.label}
        </span>
      )}
    </div>
  );
}
