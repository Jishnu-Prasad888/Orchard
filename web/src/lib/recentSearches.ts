/**
 * The last few searches, remembered on this device so the search box can offer
 * them again. Per browser, never sent anywhere.
 */

const KEY = 'cairn.recentSearches';
const LIMIT = 6;

export function readRecentSearches(): string[] {
  try {
    const raw = localStorage.getItem(KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === 'string') : [];
  } catch {
    return [];
  }
}

export function rememberSearch(query: string): void {
  const q = query.trim();
  if (!q) return;
  const next = [q, ...readRecentSearches().filter((v) => v.toLowerCase() !== q.toLowerCase())];
  write(next.slice(0, LIMIT));
}

export function forgetSearch(query: string): void {
  write(readRecentSearches().filter((v) => v !== query));
}

function write(list: string[]): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(list));
  } catch {
    // Not remembered; nothing else depends on it.
  }
}
