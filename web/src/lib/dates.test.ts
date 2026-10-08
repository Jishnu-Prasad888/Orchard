import { describe, expect, it } from 'vitest';

import { dayKey, formatDayHeading, greeting, monthKey } from './dates';

describe('dates', () => {
  const now = new Date(2026, 8, 30, 10);

  it('keys a timestamp by local day and month', () => {
    expect(dayKey('2026-09-28T12:00:00')).toBe('2026-09-28');
    expect(monthKey('2026-09-28T12:00:00')).toBe('2026-09');
    expect(dayKey('not a date')).toBe('');
  });

  it('says Today and Yesterday for the last two days', () => {
    expect(formatDayHeading('2026-09-30', now)).toBe('Today');
    expect(formatDayHeading('2026-09-29', now)).toBe('Yesterday');
  });

  it('adds the year only for other years', () => {
    expect(formatDayHeading('2026-03-04', now)).not.toMatch(/2026/);
    expect(formatDayHeading('2025-03-04', now)).toMatch(/2025/);
  });

  it('labels an unparseable day as undated', () => {
    expect(formatDayHeading('', now)).toBe('Undated');
  });

  it('greets by the hour', () => {
    expect(greeting(new Date(2026, 0, 1, 9))).toBe('Good morning');
    expect(greeting(new Date(2026, 0, 1, 14))).toBe('Good afternoon');
    expect(greeting(new Date(2026, 0, 1, 20))).toBe('Good evening');
  });
});
