import { describe, expect, test } from "vitest";
import * as normalHost from "@/lib/host";
import * as demoHost from "./host";
import { DemoServer, REFUSED } from "./server";
import { profilesOf, seed } from "./seed";

// The demo's host has the same exports as the real one, with the same types.
const sameShape: typeof normalHost = demoHost;

const get = async (server: DemoServer, path: string) => {
  const response = await server.handle(path);
  return { status: response.status, body: await response.json(), position: response.headers.get("X-Agentry-Position") };
};

// What each page reads, with the demo's own names in the paths.
const PAGES: Record<string, string[]> = {
  shell: ["/api/tasks", "/api/questions", "/api/agents", "/api/config", "/api/features"],
  tasks: ["/api/tasks", "/api/agents", "/api/settings"],
  plan: ["/api/tasks"],
  agents: ["/api/agents", "/api/teams/templates", "/api/policy"],
  agent: ["/api/agents", "/api/agents/engineer/knowledge", "/api/policy", "/api/config"],
  workers: ["/api/workers", "/api/workers/build-box", "/api/config"],
  integrations: ["/api/integrations/connections"],
  policy: ["/api/policy", "/api/policy/history", "/api/policy/history/3"],
  settings: ["/api/settings", "/api/features", "/api/push"],
  task: [
    "/api/tasks/task-12",
    "/api/tasks/task-12/commits",
    "/api/tasks/task-12/events?after=0",
    "/api/tasks/task-12/events?before=1000",
    "/api/tasks/task-12/events?type=task.created&type=task.assigned&type=event.redacted&after=0",
  ],
  workspace: [
    "/api/tasks/task-12",
    "/api/tasks/task-12/files",
    "/api/tasks/task-12/commits",
    "/api/tasks/task-12/file?path=src%2Fwebhooks%2Fsend.ts",
  ],
};

// The page's source, but for the demo and the tests.
const SOURCES: Record<string, string> = import.meta.glob(
  ["/src/**/*.ts", "/src/**/*.tsx", "!/src/**/*.test.*", "!/src/demo/**", "!/src/**/*fixtures*"],
  {
    query: "?raw",
    import: "default",
    eager: true,
  },
);

// Every source file but the tests, the demo and the host: all the page's
// own talk with the network and the browser's storage goes through the host.
const BEYOND_HOST: Record<string, string> = import.meta.glob(
  ["/src/**/*.ts", "/src/**/*.tsx", "!/src/**/*.test.*", "!/src/demo/**", "!/src/lib/host.ts"],
  { query: "?raw", import: "default", eager: true },
);

const PAST_THE_HOST =
  /(?<![\w.])fetch\(|\b(?:window|globalThis|self)\.fetch\(|new WebSocket\b|localStorage|sessionStorage|serviceWorker\.register/g;

// The API paths the page's source reads that need no name filled in.
function literalReads(): string[] {
  const paths = new Set<string>();
  for (const source of Object.values(SOURCES))
    for (const match of source.matchAll(/(?:useResource<[^>]*>|api<[^>]*>|api)\(\s*(?:"GET",\s*)?"(\/api\/[^"?]+)"/g)) paths.add(match[1]);
  return [...paths].sort();
}

describe("the demo's pretend server", () => {
  test.each(Object.entries(PAGES))("answers every read of %s", async (_, paths) => {
    const server = new DemoServer();
    for (const path of paths) {
      const { status, body, position } = await get(server, path);
      expect([path, status]).toEqual([path, 200]);
      expect(body).toBeDefined();
      expect(Number(position)).toBeGreaterThan(0);
    }
  });

  test("answers every API path the page's source reads", async () => {
    const server = new DemoServer();
    const reads = literalReads();
    expect(reads.length).toBeGreaterThan(10);
    for (const path of reads.filter((p) => p !== "/api/changes")) expect([path, (await server.handle(path)).status]).toEqual([path, 200]);
  });

  test("is the only way out: no source past the host fetches, opens a socket, stores or registers a worker", () => {
    expect(Object.keys(BEYOND_HOST).length).toBeGreaterThan(100);
    const found = Object.entries(BEYOND_HOST).flatMap(([file, source]) =>
      [...source.matchAll(PAST_THE_HOST)].map((m) => `${file}: ${m[0]}`),
    );
    expect(found).toEqual([]);
  });

  test("answers each task's page and events", async () => {
    const server = new DemoServer();
    for (const task of server.data.tasks) {
      expect((await get(server, `/api/tasks/${task.id}`)).body.id).toBe(task.id);
      const { body } = await get(server, `/api/tasks/${task.id}/events?after=0`);
      expect(body.more).toBe(false);
      expect(body.events.length).toBe(server.data.events.get(task.id)!.length);
    }
  });

  test("pages events by type and position, oldest first", async () => {
    const server = new DemoServer();
    const all = (await get(server, "/api/tasks/task-12/events?after=0")).body.events as { position: number; type: string }[];
    const handoffs = (await get(server, "/api/tasks/task-12/events?type=task.assigned&after=0")).body.events as { type: string }[];
    expect(handoffs.length).toBe(2);
    expect(handoffs.every((e) => e.type === "task.assigned")).toBe(true);
    const before = (await get(server, `/api/tasks/task-12/events?before=${all[3].position}`)).body.events;
    expect(before).toEqual(all.slice(0, 3));
    expect(all.map((e) => e.position)).toEqual([...all.map((e) => e.position)].sort((a, b) => a - b));
  });

  test("lists what waits for you: the push to approve and the question", async () => {
    const { body } = await get(new DemoServer(), "/api/questions");
    expect(body.map((q: { kind: string; task: string; text: string }) => [q.kind, q.task, q.text])).toEqual([
      ["question", "task-10", expect.stringContaining("tracking number")],
      ["approval", "task-11", "git push origin dev"],
    ]);
  });

  test("refuses a path it doesn't have, and says which", async () => {
    const server = new DemoServer();
    expect(await get(server, "/api/nothing")).toMatchObject({ status: 404, body: { error: "The demo has no /api/nothing." } });
    expect(await get(server, "/api/tasks/task-99")).toMatchObject({ status: 404, body: { error: "No task is called task-99." } });
  });

  // Only a new task, an answer and a yes or no go to the simulator.
  test.each([
    ["POST", "/api/tasks/task-12/message"],
    ["POST", "/api/tasks/task-12/direct"],
    ["PUT", "/api/tasks/task-12/rank"],
    ["PUT", "/api/tasks"],
    ["DELETE", "/api/tasks/task-12"],
    ["PUT", "/api/agents/engineer"],
    ["POST", "/api/policy/revert"],
    ["PUT", "/api/settings"],
    ["POST", "/api/teams"],
    ["POST", "/api/workers/invite"],
    ["POST", "/api/ideas/idea-1/accept"],
    ["POST", "/api/redact"],
    ["POST", "/api/tasks/task-12/erase"],
  ])("refuses any other change, such as %s %s: nothing runs", async (method, path) => {
    const server = new DemoServer();
    const position = server.data.log.position;
    const response = await server.handle(path, { method, body: "{}" });
    expect(response.status).toBe(403);
    expect(await response.json()).toEqual({ error: REFUSED });
    expect(server.data.log.position).toBe(position);
  });

  test("answers the long poll at once when behind, and waits when caught up", async () => {
    const server = new DemoServer();
    const now = server.data.log.position;
    expect((await get(server, "/api/changes?after=0")).body).toEqual({ position: now });
    let answered = false;
    const waiting = server.handle(`/api/changes?after=${now}`).then((r) => r.json().then((body) => ((answered = true), body)));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(answered).toBe(false);
    server.move();
    expect(await waiting).toEqual({ position: now + 1 });
  });

  test("waits not at all when the page has given up already", async () => {
    const server = new DemoServer();
    const waiting = server.handle(`/api/changes?after=${server.data.log.position}`, { signal: AbortSignal.abort() });
    await expect(waiting).rejects.toMatchObject({ name: "AbortError" });
    expect(server["moved"].size).toBe(0);
  });

  test("stops waiting when the page gives up", async () => {
    const server = new DemoServer();
    const stop = new AbortController();
    const waiting = server.handle(`/api/changes?after=${server.data.log.position}`, { signal: stop.signal });
    stop.abort();
    await expect(waiting).rejects.toMatchObject({ name: "AbortError" });
  });
});

describe("the demo's host", () => {
  test("keeps the page's settings in memory, never in the browser", () => {
    expect(sameShape).toBe(demoHost);
    localStorage.clear();
    sessionStorage.clear();
    demoHost.storage().setItem("agentry-theme", "light");
    expect(demoHost.storage().getItem("agentry-theme")).toBe("light");
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  test("answers calls itself, and opens pages in the address's hash", async () => {
    expect((await demoHost.request("/api/config")).status).toBe(200);
    expect(demoHost.pageURL("/tasks/task-12/workspace")).toBe("#/tasks/task-12/workspace");
  });
});

describe("the demo's data", () => {
  test("is generic: no people, home folders, addresses or keys", () => {
    const { policy: _policy, agents, ...rest } = seed();
    const text =
      JSON.stringify(rest, (_, value) => (value instanceof Map ? [...value] : value)) +
      JSON.stringify(agents.map(({ prompt: _prompt, ...a }) => a));
    expect(text).not.toMatch(/\/Users\/|\/home\/|[\w.]+@[\w-]+\.\w+|\bghp_|\bsk-[A-Za-z0-9]{16}|token=/);
  });

  test("reads every profile of the policy Agentry ships, with its rules", () => {
    const { policy } = seed();
    expect(policy.profiles.map((p) => p.name)).toEqual(expect.arrayContaining(["developer", "read-only"]));
    expect(policy.profiles.every((p) => p.why && p.rules.every((r) => r.name && r.decision && r.why))).toBe(true);
    expect(
      profilesOf('[profiles.x]\nwhy = "w"\notherwise = "deny"\n  [[profiles.x.rules]]\n  name = "n"\n  decision = "allow"\n  why = "y"\n'),
    ).toEqual([{ name: "x", why: "w", otherwise: "deny", rules: [{ name: "n", decision: "allow", why: "y" }] }]);
  });

  test("starts fresh on each load", () => {
    const first = new DemoServer();
    first.move();
    expect(new DemoServer().data.log.position).toBe(first.data.log.position - 1);
  });
});
