/**
 * The layout breakpoints, mirrored from `styles/tokens.css`. CSS media queries
 * cannot read custom properties, so the numbers live in both places; these are
 * for the few layout decisions made in script (grid tile sizes).
 */
export const BREAKPOINTS = {
  /** Below this: phones. Bottom navigation, compact grid. */
  compact: 600,
  /** Below this: tablets. Navigation drawer. */
  medium: 900,
  /** Below this: small laptops. */
  expanded: 1200,
} as const;
