// The demo's pretend `agentry serve`, in the browser: it answers each call
// the page makes from the seed, as the real server would, with the log
// position in X-Agentry-Position. Nothing leaves the page. Three writes go
// to the simulator: a new task, an answer, and a yes or no to a command.
// Every other write is refused, with a reason the page shows.

import type { Question, TaskDetail } from "@/api/types";
import { seed, Seed } from "./seed";
import { MOST_NEW, Simulator } from "./sim";
import { summaryOf } from "./story";

export const REFUSED = "This is a demo: nothing runs. Download Agentry to use it.";
export const TOO_MANY = `The demo makes at most ${MOST_NEW} new tasks a visit. Download Agentry to make as many as you like.`;
export const NO_EPICS = "The demo runs single tasks, not epics or their stories. Download Agentry to plan an epic.";
const PAGE = 500; // events in a page, as the server's

type Handler = (match: RegExpExecArray, query: URLSearchParams) => unknown;
type Write = (match: RegExpExecArray, body: Record<string, unknown>) => unknown;

export class DemoServer {
  readonly data: Seed;
  readonly sim: Simulator;
  private moved = new Set<() => void>();
  private routes: [RegExp, Handler][];
  private writes: [RegExp, Write][];

  constructor(data: Seed = seed(), wait?: () => number) {
    this.data = data;
    this.sim = new Simulator(data, () => this.move(), wait);
    this.writes = [
      [/^\/api\/tasks$/, (_, body) => this.create(body)],
      [/^\/api\/tasks\/([^/]+)\/answer$/, (m, body) => this.answer(m[1], body)],
      [/^\/api\/tasks\/([^/]+)\/actions\/([^/]+)\/(approve|deny)$/, (m, body) => this.decide(m[1], m[2], m[3] === "approve", body)],
    ];
    const d = data;
    this.routes = [
      [/^\/api\/tasks$/, () => d.tasks.map(summaryOf)],
      [/^\/api\/tasks\/([^/]+)$/, (m) => this.task(m[1])],
      [/^\/api\/tasks\/([^/]+)\/events$/, (m, q) => this.events(m[1], q)],
      [
        /^\/api\/tasks\/([^/]+)\/files$/,
        (m) => (this.task(m[1]), { base: "dev", files: d.files.map(({ path, change }) => ({ path, change })) }),
      ],
      [/^\/api\/tasks\/([^/]+)\/commits$/, (m) => (this.task(m[1]), d.commits)],
      [/^\/api\/tasks\/([^/]+)\/file$/, (m, q) => (this.task(m[1]), this.file(q.get("path") ?? ""))],
      [/^\/api\/questions$/, () => d.tasks.flatMap(questionsOf)],
      [/^\/api\/agents$/, () => d.agents],
      [/^\/api\/agents\/([^/]+)\/knowledge$/, (m) => d.knowledge.get(m[1]) ?? []],
      [/^\/api\/teams\/templates$/, () => d.templates],
      [/^\/api\/config$/, () => d.config],
      [/^\/api\/settings$/, () => d.settings],
      [/^\/api\/features$/, () => d.features],
      [/^\/api\/policy$/, () => d.policy],
      [/^\/api\/policy\/history$/, () => d.history],
      [/^\/api\/policy\/history\/(\d+)$/, (m) => this.version(Number(m[1]))],
      [/^\/api\/workers$/, () => d.workers],
      [/^\/api\/workers\/([^/]+)$/, (m) => d.workerDetails.get(m[1]) ?? notFound(`No machine is called ${m[1]}.`)],
      [/^\/api\/integrations\/connections$/, () => ({ connections: d.connections })],
      [/^\/api\/push$/, () => ({ available: false, reason: "The demo sends no notifications.", subscribed: 0 })],
      [/^\/api\/ideas$/, () => []],
    ];
  }

  // handle answers one call, as fetch would.
  async handle(path: string, init: RequestInit = {}): Promise<Response> {
    const url = new URL(path, "http://demo.invalid");
    const method = (init.method ?? "GET").toUpperCase();
    if (method === "GET" && url.pathname === "/api/changes") return this.changes(Number(url.searchParams.get("after") ?? 0), init.signal);
    const write = method === "POST" ? this.writes.find(([pattern]) => pattern.test(url.pathname)) : undefined;
    if (method !== "GET" && !write) return reply(403, { error: REFUSED });
    try {
      if (write) return reply(200, write[1](write[0].exec(url.pathname)!, bodyOf(init.body)), this.data.log.position);
      for (const [pattern, handler] of this.routes) {
        const match = pattern.exec(url.pathname);
        if (match) return reply(200, handler(match, url.searchParams), this.data.log.position);
      }
    } catch (failure) {
      if (failure instanceof Refusal) return reply(failure.status, { error: failure.message });
      throw failure;
    }
    return reply(404, { error: `The demo has no ${url.pathname}.` });
  }

  // move tells the long poll the log moved, as a change to the data would.
  move() {
    this.data.log.next();
    this.moved.forEach((wake) => wake());
  }

  // changes answers the long poll at once when the log is past after, and
  // otherwise waits for it to move, or for the page to give up.
  private changes(after: number, signal?: AbortSignal | null): Promise<Response> {
    const answer = () => reply(200, { position: this.data.log.position }, this.data.log.position);
    if (this.data.log.position > after) return Promise.resolve(answer());
    return new Promise((resolve, reject) => {
      const stopped = () => reject(new DOMException("The page stopped waiting.", "AbortError"));
      if (signal?.aborted) return stopped();
      const wake = () => {
        this.moved.delete(wake);
        resolve(answer());
      };
      this.moved.add(wake);
      signal?.addEventListener("abort", () => {
        this.moved.delete(wake);
        stopped();
      });
    });
  }

  // create makes a task, as the real server does, with the answer it gives.
  private create(body: Record<string, unknown>) {
    const title = text(body.title);
    const description = text(body.description);
    if (!title && !description) throw new Refusal("describe the task", 400);
    if (body.kind === "epic" || text(body.epic) || (Array.isArray(body.stories) && body.stories.length)) throw new Refusal(NO_EPICS, 403);
    if (this.sim.made >= MOST_NEW) throw new Refusal(TOO_MANY, 403);
    const story = this.sim.create(title || titleFrom(description), description);
    return { stream: story.summary.id, position: story.events[0].position, notFiled: [] };
  }

  // answer and decide refuse what the real server's rules refuse, with
  // their words.
  private answer(id: string, body: Record<string, unknown>) {
    if (!this.story(id).asking) throw new Refusal("the task isn't waiting for an answer", 409);
    return this.sim.answer(decodeURIComponent(id), text(body.answer));
  }

  private decide(id: string, actionId: string, approved: boolean, body: Record<string, unknown>) {
    const name = decodeURIComponent(actionId);
    const action = this.story(id).action(name);
    if (!action) throw new Refusal(`action ${name} doesn't exist`, 409);
    if (action.decision) throw new Refusal(`action ${name} was already ${action.decision}`, 409);
    return this.sim.decide(decodeURIComponent(id), approved, text(body.reason) || "no reason given");
  }

  private story(id: string) {
    const name = decodeURIComponent(id);
    return this.data.stories.get(name) ?? notFound(`no such task ${name}`);
  }

  private task(id: string): TaskDetail {
    return this.data.tasks.find((t) => t.id === decodeURIComponent(id)) ?? notFound(`No task is called ${decodeURIComponent(id)}.`);
  }

  // events is a page of a task's events: those of the types asked for, after
  // or before a position, oldest first.
  private events(id: string, query: URLSearchParams) {
    const task = this.task(id);
    const types = query.getAll("type");
    const all = (this.data.events.get(task.id) ?? []).filter((e) => !types.length || types.includes(e.type));
    if (query.has("before")) {
      const before = all.filter((e) => e.position < Number(query.get("before")));
      return { events: before.slice(-PAGE), more: before.length > PAGE };
    }
    const after = all.filter((e) => e.position > Number(query.get("after") ?? 0));
    return { events: after.slice(0, PAGE), more: after.length > PAGE };
  }

  private file(path: string) {
    const file = this.data.files.find((f) => f.path === path) ?? notFound(`No file ${path} in the workspace.`);
    return { path, now: file.now, before: file.change === "M" ? file.before : "", binary: false, tooBig: false };
  }

  private version(position: number) {
    const version = this.data.history.find((v) => v.position === position) ?? notFound(`No policy version ${position}.`);
    return { position, at: version.at, diff: "", useForEveryone: "", inForceUseForEveryone: "" };
  }
}

class Refusal extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

function notFound(message: string): never {
  throw new Refusal(message, 404);
}

function bodyOf(body: RequestInit["body"]): Record<string, unknown> {
  try {
    const parsed: unknown = typeof body === "string" ? JSON.parse(body) : {};
    return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

const text = (value: unknown) => (typeof value === "string" ? value.trim() : "");

// titleFrom stands in for a title nobody gave, as the real server's: the
// description's first line, cut at a word to at most 72 characters.
function titleFrom(description: string): string {
  const line = description.split("\n")[0].split(/\s+/).filter(Boolean).join(" ");
  if (line.length <= 72) return line;
  const space = line.lastIndexOf(" ", 71);
  return `${line.slice(0, space < 40 ? 72 : space).replace(/[,.;:]+$/, "")}...`;
}

function reply(status: number, body: unknown, position?: number): Response {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (position !== undefined) headers["X-Agentry-Position"] = String(position);
  return new Response(JSON.stringify(body), { status, headers });
}

// questionsOf is what a task waits on you for, as the server lists it: its
// approvals, then its open question.
function questionsOf(t: TaskDetail): Question[] {
  const context = [...(t.trail ?? [])].reverse().find((s) => s.to !== "you");
  const base = (kind: Question["kind"], key: string): Question => ({
    id: `${t.id}/${kind}/${key}`,
    kind,
    task: t.id,
    taskTitle: t.title,
    agent: "",
    at: "",
    text: "",
    context: context ? `${context.from} → ${context.to}: ${context.text}` : "",
  });
  const questions: Question[] = (t.approvals ?? []).map((a) => ({
    ...base("approval", a.id),
    agent: a.agent,
    at: a.requestedAt,
    text: a.command,
    action: a.id,
    tool: a.tool,
    reason: a.reason,
  }));
  if (t.hasQuestion) {
    const asked = [...(t.trail ?? [])].reverse().find((s) => s.to === "you");
    questions.push({
      ...base("question", "open"),
      agent: t.coordinator,
      at: asked?.at ?? t.lastActiveAt,
      text: t.needsYou,
      options: t.options,
    });
  }
  return questions;
}
