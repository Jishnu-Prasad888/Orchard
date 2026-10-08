/**
 * Whether the server has ffmpeg, which it needs to make video thumbnails.
 *
 * `null` while unknown (nothing is warned about on a guess), then true/false.
 * Read once per page load and shared by the sign-in toast and Settings.
 */

import { useEffect, useState } from 'react';

import { getSystemCapabilities } from './queries';

import type { SystemCapabilities } from './queries';

let pending: Promise<SystemCapabilities | null> | null = null;

function load(): Promise<SystemCapabilities | null> {
  pending ??= getSystemCapabilities().then(
    (c) => c,
    () => null,
  );
  return pending;
}

/** What the server has: `null` while unknown. */
export function useSystemCapabilities(): SystemCapabilities | null {
  const [value, setValue] = useState<SystemCapabilities | null>(null);
  useEffect(() => {
    let live = true;
    void load().then((v) => {
      if (live) setValue(v);
    });
    return () => {
      live = false;
    };
  }, []);
  return value;
}

export function useFfmpegAvailable(): boolean | null {
  return useSystemCapabilities()?.ffmpeg ?? null;
}
