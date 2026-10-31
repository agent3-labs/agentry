import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { LogEvent, Question, TaskDetail, TaskSummary } from "@/api/types";
import { DemoServer, NO_EPICS, TOO_MANY } from "./server";

// The simulator plays a task on once you act, a beat every few seconds, and
// every read of the task agrees at each beat.

const get = async <T>(server: DemoServer, path: string): Promise<T> => (await server.handle(path)).json() as Promise<T>;
const post = async (server: DemoServer, path: string, body: unknown = {}) => {
  const response = await server.handle(path, { method: "POST", body: JSON.stringify(body) });
  return { status: response.status, body: await response.json() };
};
const detail = (server: DemoServer, id: string) => get<TaskDetail>(server, `/api/tasks/${id}`);
const questions = (server: DemoServer) => get<Question[]>(server, "/api/questions");
const pushOf = async (server: DemoServer, id: string) => (await detail(server, id)).approvals![0].id;

// play runs every beat to come, checking after each that the reads agree.
async function play(server: DemoServer, id: string) {
  while (vi.getTimerCount()) {
    vi.advanceTimersToNextTimer();
    await agree(server, id);
  }
}

// agree checks every tab's read of a task tells the same story.
async function agree(server: DemoServer, id: string) {
  const task = await detail(server, id);
  const { events } = await get<{ events: LogEvent[] }>(server, `/api/tasks/${id}/events?after=0`);
  const listed = (await get<TaskSummary[]>(server, "/api/tasks")).find((t) => t.id === id)!;
  const asked = (await questions(server)).filter((q) => q.task === id);
  const count = (type: string) => events.filter((e) => e.type === type).length;
  const turns = task.turnList ?? [];
  expect(turns.length).toBe(count("turn.started"));
  expect(turns.filter((t) => t.ended).length).toBe(count("turn.ended"));
  expect(task.currentAgent).toBe(turns.find((t) => !t.ended)?.agent ?? "");
  for (const action of task.actions ?? [])
    expect(events.some((e) => e.type === "action.requested" && e.correlation === action.id)).toBe(true);
  expect((task.trail ?? []).filter((s) => s.from === "tech-lead" && s.to !== "you").length).toBe(count("task.assigned"));
  expect(asked.map((q) => q.kind)).toEqual([...(task.approvals ?? []).map(() => "approval"), ...(task.hasQuestion ? ["question"] : [])]);
  expect(listed).toMatchObject({
    status: task.status,
    column: task.column,
    needsYou: task.needsYou,
    turns: task.turns,
    pushed: task.pushed,
  });
  expect(events.map((e) => e.position)).toEqual([...events.map((e) => e.position)].sort((a, b) => a - b));
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0.5); // a beat every 5.5 s
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("the demo's simulator", () => {
  test("moves nothing before the visitor acts: task-11 waits on its push and task-12 stays as it is", async () => {
    const server = new DemoServer();
    const position = server.data.log.position;
    const before = await detail(server, "task-12");
    vi.advanceTimersByTime(10 * 60_000);
    expect(server.data.log.position).toBe(position);
    expect(vi.getTimerCount()).toBe(0);
    expect(await detail(server, "task-12")).toEqual(before);
    expect((await detail(server, "task-11")).approvals?.map((a) => a.command)).toEqual(["git push origin dev"]);
  });

  test("a new task runs from the coordinator to the deployer's push in about a minute, then approve ships it", async () => {
    const server = new DemoServer();
    const made = await post(server, "/api/tasks", { title: "Add a dark mode", description: "Let people pick a dark theme." });
    expect(made).toEqual({ status: 200, body: { stream: "task-15", position: expect.any(Number), notFiled: [] } });
    expect((await detail(server, "task-15")).title).toBe("Add a dark mode");

    const started = Date.now();
    await play(server, "task-15");
    expect(Date.now() - started).toBeGreaterThanOrEqual(30_000);
    expect(Date.now() - started).toBeLessThanOrEqual(120_000);
    const waiting = await detail(server, "task-15");
    expect(waiting.trail!.filter((s) => s.from === "tech-lead" && s.to !== "you").map((s) => s.to)).toEqual([
      "engineer",
      "reviewer",
      "tester",
      "deployer",
    ]);
    expect(waiting).toMatchObject({
      status: "needs you",
      column: "shipping",
      currentAgent: "deployer",
      needsYou: "Approve: git push origin dev",
    });

    const id = await pushOf(server, "task-15");
    const approved = await post(server, `/api/tasks/task-15/actions/${id}/approve`, { reason: "decided on the page" });
    expect(approved).toMatchObject({ status: 200, body: { type: "action.approved", actor: "human:you", correlation: id } });
    expect((await questions(server)).filter((q) => q.task === "task-15")).toEqual([]);
    await play(server, "task-15");
    const shipped = await detail(server, "task-15");
    expect(shipped).toMatchObject({ status: "done", column: "done", pushed: true, currentAgent: "" });
    expect(shipped.turnList!.filter((t) => t.plan).every((t) => !t.plan.includes("[ ]"))).toBe(true); // each plan ticked
    expect(vi.getTimerCount()).toBe(0);
  });

  test("a running turn shows as running until its next beat", async () => {
    const server = new DemoServer();
    await post(server, "/api/tasks", { description: "Show the stock left on each product." });
    vi.advanceTimersToNextTimer(); // the handoff
    vi.advanceTimersToNextTimer(); // the engineer starts
    expect(await detail(server, "task-15")).toMatchObject({ status: "working", column: "in-progress", currentAgent: "engineer" });
    vi.advanceTimersToNextTimer(); // and reports
    expect((await detail(server, "task-15")).currentAgent).toBe("");
  });

  test("denying task-11's push closes it with the branch left unpushed", async () => {
    const server = new DemoServer();
    const id = await pushOf(server, "task-11");
    expect(await post(server, `/api/tasks/task-11/actions/${id}/deny`)).toMatchObject({ status: 200, body: { type: "action.denied" } });
    expect((await questions(server)).filter((q) => q.task === "task-11")).toEqual([]);
    await play(server, "task-11");
    const task = await detail(server, "task-11");
    expect(task).toMatchObject({ status: "done", column: "done", pushed: false });
    expect(task.summary).toContain("agentry/task-11 is left unpushed");
    expect(task.actions!.find((a) => a.id === id)?.decision).toBe("denied");
    expect(vi.getTimerCount()).toBe(0);
  });

  test("an answer to task-10 leaves Questions at once and runs it on with the choice, to its push and done", async () => {
    const server = new DemoServer();
    const answered = await post(server, "/api/tasks/task-10/answer", { answer: "Wrap it" });
    expect(answered).toMatchObject({ status: 200, body: { type: "task.answered", data: { answer: "Wrap it" } } });
    expect((await questions(server)).filter((q) => q.task === "task-10")).toEqual([]);
    await play(server, "task-10");
    const task = await detail(server, "task-10");
    expect(task.trail!.findLast((s) => s.to === "engineer")?.text).toBe(
      'The user answered: "Wrap it". Make the status box fit at 320 wide that way, with a test.',
    );
    expect(task.needsYou).toBe("Approve: git push origin dev");
    await post(server, `/api/tasks/task-10/actions/${await pushOf(server, "task-10")}/approve`);
    await play(server, "task-10");
    expect(await detail(server, "task-10")).toMatchObject({ status: "done", pushed: true });
  });

  test("approving or denying twice, or what doesn't exist, is refused as the real server refuses it", async () => {
    const server = new DemoServer();
    const id = await pushOf(server, "task-11");
    await post(server, `/api/tasks/task-11/actions/${id}/approve`);
    expect(await post(server, `/api/tasks/task-11/actions/${id}/approve`)).toEqual({
      status: 409,
      body: { error: `action ${id} was already approved` },
    });
    expect(await post(server, `/api/tasks/task-11/actions/${id}/deny`)).toEqual({
      status: 409,
      body: { error: `action ${id} was already approved` },
    });
    expect(await post(server, "/api/tasks/task-11/actions/toolu_999/deny")).toEqual({
      status: 409,
      body: { error: "action toolu_999 doesn't exist" },
    });
    expect(await post(server, "/api/tasks/task-12/actions/toolu_001/approve")).toEqual({
      status: 409,
      body: { error: "action toolu_001 was already approved" },
    });
    expect(await post(server, "/api/tasks/task-99/actions/toolu_001/approve")).toEqual({
      status: 404,
      body: { error: "no such task task-99" },
    });
  });

  test("an answer to a task not waiting for one is refused as the real server refuses it", async () => {
    const server = new DemoServer();
    await post(server, "/api/tasks/task-10/answer", { answer: "Wrap it" });
    expect(await post(server, "/api/tasks/task-10/answer", { answer: "Wrap it" })).toEqual({
      status: 409,
      body: { error: "the task isn't waiting for an answer" },
    });
    expect(await post(server, "/api/tasks/task-12/answer", { answer: "yes" })).toEqual({
      status: 409,
      body: { error: "the task isn't waiting for an answer" },
    });
    expect(await post(server, "/api/tasks/task-99/answer", { answer: "yes" })).toEqual({
      status: 404,
      body: { error: "no such task task-99" },
    });
  });

  test("makes at most 5 new tasks a visit, and refuses an epic or an empty task", async () => {
    const server = new DemoServer();
    expect(await post(server, "/api/tasks", { title: " ", description: "" })).toEqual({
      status: 400,
      body: { error: "describe the task" },
    });
    expect(await post(server, "/api/tasks", { description: "Plan a loyalty scheme", kind: "epic" })).toEqual({
      status: 403,
      body: { error: NO_EPICS },
    });
    expect(await post(server, "/api/tasks", { description: "A story", epic: "task-5" })).toEqual({
      status: 403,
      body: { error: NO_EPICS },
    });
    for (let i = 1; i <= 5; i++) expect((await post(server, "/api/tasks", { description: `Task ${i}` })).status).toBe(200);
    const count = (await get<TaskSummary[]>(server, "/api/tasks")).length;
    expect(await post(server, "/api/tasks", { description: "One more" })).toEqual({ status: 403, body: { error: TOO_MANY } });
    expect((await get<TaskSummary[]>(server, "/api/tasks")).length).toBe(count);
    expect(server.sim.moving).toBe(5); // one timer for each moving task
  });

  test("a task with no title takes its description's first line, cut at a word", async () => {
    const server = new DemoServer();
    const long = "When a webhook delivery fails with a 503 the sender gives up at once, retry it once after a second\nMore detail.";
    await post(server, "/api/tasks", { description: long });
    expect((await detail(server, "task-15")).title).toBe("When a webhook delivery fails with a 503 the sender gives up at once...");
  });

  test("a new task wakes the long poll", async () => {
    const server = new DemoServer();
    const after = server.data.log.position;
    const waiting = server.handle(`/api/changes?after=${after}`);
    await post(server, "/api/tasks", { description: "Add a sitemap" });
    expect((await (await waiting).json()).position).toBeGreaterThan(after);
  });
});
