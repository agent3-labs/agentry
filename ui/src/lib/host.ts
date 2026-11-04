// What the page needs from where it runs: the server it talks to, the
// browser's storage and service worker, and its own addresses. The demo build
// (vite.demo.config.ts) swaps this file for src/demo/host.tsx, a pretend
// server in the browser, so this build never holds any of the demo.

import { createBrowserRouter } from "react-router-dom";
import type { ReactNode } from "react";

// request is one call to `agentry serve`.
export const request = (path: string, init?: RequestInit): Promise<Response> => fetch(path, init);

// openSocket opens a terminal's websocket.
export const openSocket = (url: string): WebSocket => new WebSocket(url);

// storage is where the viewer's own settings live. Reading it can throw, as
// in a private window, so callers read it inside a try.
export const storage = (): Storage => localStorage;

export const registerWorker = (path: string): Promise<unknown> => navigator.serviceWorker.register(path);

// The server answers every page's address with index.html, so the pages
// have plain addresses.
export const createRouter = createBrowserRouter;

// pageURL is the address of one of the page's own pages, to open in a new tab.
export const pageURL = (path: string): string => path;

// Frame holds every page.
export function Frame({ children }: { children: ReactNode }) {
  return children;
}
