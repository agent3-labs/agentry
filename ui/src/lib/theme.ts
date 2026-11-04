// The theme is the class on <html>: none for dark, "light" for light.
// The choice is the viewer's own, so it lives in this browser; the server
// is told too, so the sessions it starts suit the terminals showing them.

import { api } from "@/api/client";
import { storage } from "@/lib/host";

export type Theme = "dark" | "light";

const key = "agentry-theme";

export function savedTheme(): Theme {
  try {
    return storage().getItem(key) === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle("light", theme === "light");
  // The installed app's title bar, the colour of the top bar (surface-1).
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", theme === "light" ? "#fafafa" : "#16181d");
  api("POST", "/api/theme", { theme }).catch(() => {}); // not signed in yet: the next change tells it
  try {
    storage().setItem(key, theme);
  } catch {
    // a private window: the theme lasts until the tab closes
  }
}
