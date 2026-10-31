// The demo's simulator: once you act, it plays a task's next beats on a
// timer, one every few seconds, as its agents would. A new task runs from the
// coordinator to the deployer's push, an answer sends task-10 on, and a yes
// or no to a push finishes the task. Nothing moves before you act, so a link
// to a seeded task shows what it always shows.

import type { LogEvent } from "@/api/types";
import { task } from "@/api/tasks.fixtures";
import { REPO, type Seed } from "./seed";
import { Beat, Story } from "./story";

export const MOST_NEW = 5; // new tasks a visit

const randomWait = () => 3_000 + Math.random() * 5_000;

export class Simulator {
  private queues = new Map<string, Beat[]>();
  private timers = new Map<string, ReturnType<typeof setTimeout>>();
  made = 0;

  constructor(
    private data: Seed,
    private moved: () => void,
    private wait = randomWait,
  ) {}

  // moving is how many tasks have a beat to come.
  get moving(): number {
    return this.timers.size;
  }

  // create makes a new task and starts it.
  create(title: string, description: string): Story {
    const number = Math.max(...this.data.tasks.map((t) => Number(t.id.replace("task-", "")) || 0)) + 1;
    const id = `task-${number}`;
    const rank = Math.max(...this.data.tasks.map((t) => t.rank)) + 1;
    const story = new Story(this.data.log, { ...task(id, "in-progress"), title, repo: REPO, rank }, description, new Date());
    this.data.stories.set(id, story);
    this.data.events.set(id, story.events);
    this.data.tasks.push(story.detail());
    this.made++;
    this.moved();
    this.play(id, [
      { kind: "handoff", to: "engineer", text: "Build what the task asks for, with tests, and report what changed." },
      ...build(),
      ...ship(id),
    ]);
    return story;
  }

  // answer gives the coordinator your answer, and task-10 goes on with it.
  answer(id: string, text: string): LogEvent {
    this.now(id, { kind: "you", text });
    this.play(id, [
      { kind: "handoff", to: "engineer", text: `The user answered: "${text}". Make the status box fit at 320 wide that way, with a test.` },
      ...build(),
      ...ship(id),
    ]);
    return this.last(id, "task.answered");
  }

  // decide approves or denies the command the task waits on, and the
  // deployer and the coordinator finish the task.
  decide(id: string, approved: boolean, reason: string): LogEvent {
    this.now(id, { kind: "decided", approved, reason });
    this.play(id, approved ? pushed : unpushed(id));
    return this.last(id, approved ? "action.approved" : "action.denied");
  }

  private play(id: string, beats: Beat[]) {
    this.queues.set(id, [...(this.queues.get(id) ?? []), ...beats]);
    if (!this.timers.has(id))
      this.timers.set(
        id,
        setTimeout(() => this.tick(id), this.wait()),
      );
  }

  // tick plays the task's next beat, and keeps its timer only while it has
  // more to play.
  private tick(id: string) {
    this.timers.delete(id);
    const queue = this.queues.get(id) ?? [];
    const beat = queue.shift();
    if (beat) this.now(id, beat);
    if (queue.length)
      this.timers.set(
        id,
        setTimeout(() => this.tick(id), this.wait()),
      );
    else this.queues.delete(id);
  }

  // now adds a beat to the task at once, and tells the page.
  private now(id: string, beat: Beat) {
    const story = this.data.stories.get(id)!;
    story.beat(beat, Date.now());
    const at = this.data.tasks.findIndex((t) => t.id === id);
    this.data.tasks[at] = story.detail();
    this.moved();
  }

  private last(id: string, type: string): LogEvent {
    return [...this.data.stories.get(id)!.events].reverse().find((e) => e.type === type)!;
  }
}

// build is an engineer's turn on any task: generic, so it fits any title.
const build = (): Beat[] => [
  {
    kind: "running",
    agent: "engineer",
    plan: "- [x] Read the code around it\n- [ ] Make the change\n- [ ] Tests",
    commands: ["git status"],
  },
  {
    kind: "report",
    plan: "- [x] Read the code around it\n- [x] Make the change\n- [x] Tests",
    commands: ["npm test"],
    report: "Done, with 4 new tests. npm test passes.",
    tokens: 38_000,
  },
];

// ship takes a built task through review and tests to the deployer, who
// waits for your yes to the push.
const ship = (id: string): Beat[] => [
  { kind: "handoff", to: "reviewer", text: "Review the change: is it correct, clear and tested?" },
  { kind: "running", agent: "reviewer", plan: "- [ ] Read the diff\n- [ ] Check the tests", commands: ["git diff dev...HEAD"] },
  {
    kind: "report",
    plan: "- [x] Read the diff\n- [x] Check the tests",
    commands: [],
    report: "Approved: small, clear and tested.",
    tokens: 17_000,
  },
  { kind: "handoff", to: "tester", text: "Run the tests and the build, and try the change for real." },
  { kind: "running", agent: "tester", plan: "- [ ] Tests\n- [ ] Build\n- [ ] Try it", commands: ["npm test"] },
  {
    kind: "report",
    plan: "- [x] Tests\n- [x] Build\n- [x] Try it",
    commands: ["npm run build"],
    report: "Passed: all tests pass, the build is clean, and it works by hand.",
    tokens: 21_000,
  },
  { kind: "handoff", to: "deployer", text: "Merge into dev and push.", toYou: "Reviewed and tested: shipping it." },
  {
    kind: "approval",
    agent: "deployer",
    plan: "- [x] Merge into dev\n- [ ] Push",
    commands: [`git merge --no-ff agentry/${id}`, "npm test"],
    asks: "git push origin dev",
    rule: "pushes ask you",
  },
];

const pushed: Beat[] = [
  {
    kind: "report",
    plan: "- [x] Merge into dev\n- [x] Push",
    commands: ["git rev-parse HEAD"],
    report: "Pushed to dev. Its pipeline is running.",
    tokens: 9_000,
  },
  { kind: "done", summary: "Done: reviewed, tested and pushed to dev." },
];

// unpushed is the end after a denied push: the deployer stops, as its brief
// says, and the coordinator closes the task with the branch left unpushed.
const unpushed = (id: string): Beat[] => [
  { kind: "report", commands: [], report: "The push was denied, so I stopped: nothing was pushed.", tokens: 4_000 },
  { kind: "done", summary: `Closed without shipping: you denied the push, so agentry/${id} is left unpushed.` },
];
