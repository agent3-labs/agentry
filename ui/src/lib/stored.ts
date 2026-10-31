// Settings that are the viewer's own (the sidebar's width, the terminal's
// height) live in this browser only. Storage may be unavailable, as in a
// private window: then a setting lasts until the tab closes.

import { storage } from "@/lib/host";

export function readStored(key: string): string | null {
  try {
    return storage().getItem(key);
  } catch {
    return null;
  }
}

export function writeStored(key: string, value: string): void {
  try {
    storage().setItem(key, value);
  } catch {
    // not kept; see above
  }
}
