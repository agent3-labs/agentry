// The demo build's host (vite.demo.config.ts puts it in place of
// src/lib/host.ts): the page talks to a pretend server in the browser, keeps
// nothing past a reload, registers no service worker, and works from any
// folder, its pages in the address's hash. A banner on every page says so.

import { createHashRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { DemoServer } from "./server";

export const DOWNLOAD = "https://github.com/agent3-labs/agentry/releases/latest";

const server = new DemoServer();

export const request = (path: string, init?: RequestInit): Promise<Response> => server.handle(path, init);

// The demo's TerminalView opens no socket, and nothing else does.
export const openSocket = (url: string): WebSocket => {
  throw new Error(`The demo has no terminals: ${url}`);
};

// A Storage in memory, so a reload starts fresh and nothing is written to the browser.
class MemoryStorage implements Storage {
  private items = new Map<string, string>();
  get length() {
    return this.items.size;
  }
  clear() {
    this.items.clear();
  }
  getItem(key: string) {
    return this.items.get(key) ?? null;
  }
  key(index: number) {
    return [...this.items.keys()][index] ?? null;
  }
  removeItem(key: string) {
    this.items.delete(key);
  }
  setItem(key: string, value: string) {
    this.items.set(key, String(value));
  }
}

const memory = new MemoryStorage();
export const storage = (): Storage => memory;

// No service worker: the demo can't be installed and gets no pushes. Never
// settles, so the page reads it as not offered rather than refused.
export const registerWorker = (_path: string): Promise<unknown> => new Promise(() => {});

export const createRouter = createHashRouter;

export const pageURL = (path: string): string => `#${path}`;

export function Frame({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-full flex-col">
      <p
        role="note"
        className="shrink-0 border-b border-brand/40 bg-brand-soft px-3 py-1.5 text-center text-[12px] leading-5 text-ink-secondary"
      >
        Demo: nothing runs.{" "}
        <a
          href={DOWNLOAD}
          target="_blank"
          rel="noopener noreferrer"
          className="font-medium text-brand-ink underline underline-offset-2 hover:text-brand"
        >
          Download Agentry
        </a>{" "}
        to use it.
      </p>
      <div className="min-h-0 flex-1">{children}</div>
    </div>
  );
}
