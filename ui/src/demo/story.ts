// A task of the demo, told as beats: what you asked, each coordinator turn
// and its decision, each agent's turn with its commands and report. A Story
// turns the beats into the task as the server would show it: its events in
// the log, its turns, its trail and its actions, so every tab agrees. It
// grows a beat at a time, so the simulator can go on with a told task.

import type { Action, LogEvent, Step, TaskDetail, TaskSummary, Turn } from "@/api/types";
import { task } from "@/api/tasks.fixtures";

export type Beat =
  | { kind: "handoff"; to: string; text: string; toYou?: string }
  | { kind: "work"; agent: string; plan: string; commands: string[]; report: string; tokens: number }
  | { kind: "running"; agent: string; plan: string; commands: string[] } // a turn still going
  | { kind: "approval"; agent: string; plan: string; commands: string[]; asks: string; rule: string } // a turn waiting for your yes
  | { kind: "decided"; approved: boolean; reason: string } // your yes or no to the command it waits on
  | { kind: "report"; plan?: string; commands: string[]; report: string; tokens: number } // the turn still going ends
  | { kind: "ask"; question: string; options: string[]; toYou?: string }
  | { kind: "you"; text: string }
  | { kind: "done"; summary: string };

const COORDINATOR = "tech-lead";

// The board's column for the agent a task is with, as the real agents'.
const COLUMNS: Record<string, TaskSummary["column"]> = { reviewer: "review", tester: "review", deployer: "shipping" };

// The log's positions, shared by every task, as in the real log.
export class Log {
  position = 0;
  next(): number {
    return ++this.position;
  }
}

export class Story {
  readonly events: LogEvent[] = [];
  private turns: Turn[] = [];
  private trail: Step[] = [];
  private actions: Action[] = [];
  private clock: number;
  private tokens = 0;
  private open: { question: string; options: string[] } | null = null;
  private turnNumber = 0;
  private actionNumber = 0;
  private stage = "";
  private started = false;
  private summaryText = "";
  private unit = 60_000; // a step of the story: minutes, told; a moment, played live

  constructor(
    private log: Log,
    readonly summary: TaskSummary,
    readonly description: string,
    startedAt: Date,
  ) {
    this.clock = startedAt.getTime();
    this.event("task.created", "human:you", { title: summary.title, description, coordinator: COORDINATOR });
    this.step("you", COORDINATOR, description);
  }

  // waiting is the command the task waits on you for, if any.
  get waiting(): Action | undefined {
    return this.actions.find((a) => a.asked && !a.decision);
  }

  get asking(): boolean {
    return this.open !== null;
  }

  get done(): boolean {
    return this.summaryText !== "";
  }

  action(id: string): Action | undefined {
    return this.actions.find((a) => a.id === id);
  }

  // beat adds one beat, played live from now when now is given.
  beat(beat: Beat, now?: number) {
    if (now !== undefined) {
      this.clock = Math.max(this.clock, now);
      this.unit = 200;
    }
    this.started = true;
    switch (beat.kind) {
      case "handoff":
        this.decide();
        this.event("task.assigned", `agent:${COORDINATOR}`, { agent: beat.to, instructions: beat.text, toYou: beat.toYou ?? "" });
        this.stage = beat.to;
        this.step(COORDINATOR, beat.to, beat.text, `turn-${this.turnNumber + 1}`);
        break;
      case "work":
        this.turn(beat.agent, beat.plan, beat.commands, { report: beat.report, tokens: beat.tokens });
        this.step(beat.agent, COORDINATOR, beat.report, `turn-${this.turnNumber}`);
        break;
      case "running":
        this.turn(beat.agent, beat.plan, beat.commands);
        break;
      case "approval": {
        this.turn(beat.agent, beat.plan, beat.commands);
        const action = this.action_(beat.agent, beat.asks, { asked: true, decision: "", reason: beat.rule, finished: false }, 1);
        this.event("action.requested", `agent:${beat.agent}`, { tool: "Bash", command: beat.asks }, action.id);
        this.event("action.asked", "system:policy", { reason: beat.rule }, action.id);
        break;
      }
      case "decided": {
        const action = this.waiting;
        if (!action) break;
        action.decision = beat.approved ? "approved" : "denied";
        action.reason = beat.reason;
        this.event(beat.approved ? "action.approved" : "action.denied", "human:you", { reason: beat.reason }, action.id);
        if (beat.approved) {
          action.finished = true;
          this.event("action.result", `agent:${action.agent}`, {}, action.id);
        }
        break;
      }
      case "report": {
        const live = this.turns.find((t) => !t.ended);
        if (!live) break;
        if (beat.plan) {
          live.plan = beat.plan;
          this.event("plan.updated", `agent:${live.agent}`, { plan: beat.plan }, live.id);
        }
        this.run(live, beat.commands);
        this.end(live, beat.report, beat.tokens);
        this.step(live.agent, COORDINATOR, beat.report, live.id);
        break;
      }
      case "ask":
        this.decide();
        this.event("task.needs_you", `agent:${COORDINATOR}`, { question: beat.question, options: beat.options, toYou: beat.toYou ?? "" });
        this.step(COORDINATOR, "you", beat.question);
        this.open = { question: beat.question, options: beat.options };
        break;
      case "you":
        this.event(this.open ? "task.answered" : "task.directed", "human:you", this.open ? { answer: beat.text } : { text: beat.text });
        this.step("you", COORDINATOR, beat.text);
        this.open = null;
        break;
      case "done":
        this.decide();
        this.event("task.done", `agent:${COORDINATOR}`, { summary: beat.summary });
        this.step(COORDINATOR, "you", beat.summary);
        this.summaryText = beat.summary;
        break;
    }
  }

  // detail is the task as the server shows it now.
  detail(): TaskDetail {
    const { summary, turns, actions, trail, open, events } = this;
    const live = turns.find((t) => !t.ended);
    const approvals = actions.filter((a) => a.asked && !a.decision);
    const done = this.done;
    return {
      ...summary,
      status: done ? "done" : open || approvals.length ? "needs you" : summary.status,
      currentAgent: live?.agent ?? "",
      stage: this.stage || summary.stage,
      column: done ? "done" : this.stage && !summary.parked ? (COLUMNS[this.stage] ?? "in-progress") : summary.column,
      pushed: summary.pushed || actions.some((a) => a.asked && a.decision === "approved" && a.command.startsWith("git push")),
      turns: turns.length,
      tokensUsed: this.tokens,
      needsYou: open ? open.question : approvals.length ? `Approve: ${approvals[0].command}` : "",
      hasQuestion: Boolean(open),
      lastActiveAt: events[events.length - 1].at,
      createdAt: events[0].at,
      description: this.description,
      firstTitle: "",
      options: open?.options ?? [],
      page: "",
      coordinator: COORDINATOR,
      teamChosen: true,
      epicTitle: "",
      storyList: [],
      shared: [],
      movingTo: "",
      summary: this.summaryText,
      worktree: this.started
        ? { worker: "local", path: `~/.agentry/worktrees/${summary.id}`, branch: `agentry/${summary.id}`, base: "dev", port: 0 }
        : null,
      trail: trail.length ? [...trail] : null,
      turnList: turns.length ? turns.map((t) => ({ ...t })) : null,
      tokensUnknown: "",
      approvals: approvals.length ? approvals.map((a) => ({ ...a })) : null,
      actions: actions.length ? actions.map((a) => ({ ...a })) : null,
    };
  }

  private at(steps = 0): string {
    return new Date((this.clock += steps * this.unit)).toISOString();
  }

  private event(type: string, actor: string, data: Record<string, unknown> = {}, correlation?: string): LogEvent {
    const e: LogEvent = { position: this.log.next(), at: this.at(), type, actor, data };
    if (correlation) e.correlation = correlation;
    this.events.push(e);
    return e;
  }

  private step(from: string, to: string, text: string, turn = "") {
    this.trail.push({ at: this.at(), from, to, text, turn });
  }

  private action_(agent: string, command: string, how: Pick<Action, "asked" | "decision" | "reason" | "finished">, steps: number): Action {
    const action: Action = {
      id: `toolu_${String(++this.actionNumber).padStart(3, "0")}`,
      turn: `turn-${this.turnNumber}`,
      agent,
      tool: "Bash",
      command,
      requestedAt: this.at(steps),
      held: false,
      ...how,
    };
    this.actions.push(action);
    return action;
  }

  // turn runs one agent's turn: its brief, plan and commands, and its end
  // when it has one.
  private turn(agent: string, plan: string, commands: string[], end?: { report: string; tokens: number }) {
    const info: Turn = {
      id: `turn-${++this.turnNumber}`,
      agent,
      worker: "local",
      startedAt: this.at(1),
      lastEventAt: "",
      acknowledged: true,
      ended: false,
      issue: "",
      tokens: 0,
      plan,
      planFrom: "",
      stopped: false,
    };
    this.turns.push(info);
    this.event("turn.started", "system:agentry", { agent }, info.id);
    this.event("prompt.acknowledged", `agent:${agent}`, {}, info.id);
    if (plan) this.event("plan.updated", `agent:${agent}`, { plan }, info.id);
    this.run(info, commands);
    if (end) this.end(info, end.report, end.tokens);
    else info.lastEventAt = this.at();
  }

  // run runs commands the profile allows in a turn.
  private run(turn: Turn, commands: string[]) {
    for (const command of commands) {
      const action = this.action_(
        turn.agent,
        command,
        { asked: false, decision: "approved", reason: "the profile allows it", finished: true },
        2,
      );
      action.turn = turn.id;
      this.event("action.requested", `agent:${turn.agent}`, { tool: "Bash", command }, action.id);
      this.event("action.approved", "system:policy", { reason: action.reason }, action.id);
      this.event("action.result", `agent:${turn.agent}`, {}, action.id);
    }
    turn.lastEventAt = this.at();
  }

  private end(turn: Turn, report: string, tokens: number) {
    turn.ended = true;
    turn.tokens = tokens;
    this.tokens += tokens;
    this.event("turn.ended", "system:agentry", { tokens, report }, turn.id);
    turn.lastEventAt = this.at();
  }

  // The coordinator thinks in a turn of its own before each decision.
  private decide() {
    this.turn(COORDINATOR, "", [], { report: "", tokens: 6_000 + 900 * this.turnNumber });
  }
}

// tell writes a task's beats into the log, one beat every few minutes from
// startedAt, and gives back its story.
export function tell(log: Log, summary: TaskSummary, description: string, beats: Beat[], startedAt: Date): Story {
  const story = new Story(log, summary, description, startedAt);
  for (const beat of beats) story.beat(beat);
  return story;
}

// summaryOf is a task as the board lists it: the fields the fixtures' task has.
const SUMMARY = Object.keys(task("", "todo")) as (keyof TaskSummary)[];
export const summaryOf = (detail: TaskDetail): TaskSummary =>
  Object.fromEntries(SUMMARY.map((key) => [key, detail[key]])) as unknown as TaskSummary;
