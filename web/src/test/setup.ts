import '@testing-library/jest-dom/vitest';
import { configure } from '@testing-library/react';

/**
 * The default 1s async-util timeout is a real-time budget, and a real-time
 * budget is a flake generator: 24 test files rendering the whole app in
 * parallel routinely pushes a single `findBy` past it on a loaded machine, and
 * a suite that fails at random teaches people to ignore red. Nothing here
 * waits on a timer — the timeouts exist to catch a promise that never settles,
 * which a longer budget still catches.
 */
configure({ asyncUtilTimeout: 5000 });

import { afterEach, beforeEach } from 'vitest';

/**
 * Per-browser preferences (grid or list, the viewer's details panel, recent
 * searches) live in localStorage, which jsdom shares across tests in a file.
 * Starting each test clean keeps one test's choice from becoming another's
 * precondition.
 */
beforeEach(() => {
  try {
    localStorage.clear();
  } catch {
    // No storage in this environment.
  }
});

afterEach(() => {
  try {
    localStorage.clear();
  } catch {
    // No storage in this environment.
  }
});
