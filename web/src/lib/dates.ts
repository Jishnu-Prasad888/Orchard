/**
 * Calendar helpers for grouping and labelling media by day.
 *
 * Everything works in the viewer's local time zone: "today" is the reader's
 * today, and a photo taken late in the evening belongs to that evening.
 */

/** `YYYY-MM-DD` in local time, or `''` for an unparseable date. */
export function dayKey(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}

/** `YYYY-MM` in local time, or `''`. */
export function monthKey(iso: string): string {
  return dayKey(iso).slice(0, 7);
}

function parseKey(key: string): Date | null {
  const [y, m, d] = key.split('-').map(Number);
  if (!y || !m) return null;
  return new Date(y, m - 1, d || 1);
}

/**
 * A day heading as a person would say it: "Today", "Yesterday", "Mon, Sep 28"
 * this year, "September 28, 2025" before that.
 */
export function formatDayHeading(key: string, now: Date = new Date()): string {
  const date = parseKey(key);
  if (!date) return 'Undated';
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const diffDays = Math.round((today.getTime() - date.getTime()) / 86_400_000);
  if (diffDays === 0) return 'Today';
  if (diffDays === 1) return 'Yesterday';
  if (date.getFullYear() === now.getFullYear()) {
    return date.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
  }
  return date.toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' });
}

/** "September 2026", or just "September" for the current year. */
export function formatMonthHeading(key: string, now: Date = new Date()): string {
  const date = parseKey(key);
  if (!date) return 'Undated';
  return date.toLocaleDateString(
    undefined,
    date.getFullYear() === now.getFullYear()
      ? { month: 'long' }
      : { month: 'long', year: 'numeric' },
  );
}

/** A short absolute date for metadata rows and lists. */
export function formatDate(iso: string | undefined): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

/** Date and time, for "Taken" and "Modified". */
export function formatDateTime(iso: string | undefined): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}

/** "Good morning" / "Good afternoon" / "Good evening" for the Home greeting. */
export function greeting(now: Date = new Date()): string {
  const hour = now.getHours();
  if (hour < 5) return 'Good evening';
  if (hour < 12) return 'Good morning';
  if (hour < 18) return 'Good afternoon';
  return 'Good evening';
}
