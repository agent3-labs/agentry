// The demo's data: the Software team Agentry ships, with its own prompts and
// policy, one more machine, and a few tasks of a small web shop at every
// stage, from the backlog to shipped. Made fresh on each load, with times
// just before now, so the page looks live. Generic on purpose: no real
// people, companies, paths or keys.

import type {
  Agent,
  FeaturesReply,
  LogEvent,
  Policy,
  PolicyProfile,
  PolicyVersion,
  ServerConfig,
  Settings,
  TaskDetail,
  TeamTemplate,
  WorkerDetail,
  WorkerList,
} from "@/api/types";
import type { WorkspaceFile } from "@/components/molecules/FileTree";
import { task } from "@/api/tasks.fixtures";
import { Beat, Log, Story, tell } from "./story";
import techLead from "../../../internal/domain/agents/defaults/tech-lead.md?raw";
import engineer from "../../../internal/domain/agents/defaults/engineer.md?raw";
import reviewer from "../../../internal/domain/agents/defaults/reviewer.md?raw";
import tester from "../../../internal/domain/agents/defaults/tester.md?raw";
import deployer from "../../../internal/domain/agents/defaults/deployer.md?raw";
import policyText from "../../../internal/domain/policy/default.toml?raw";

export const REPO = "https://git.example.com/demo/webshop.git";
const VERSION = "0.10.1";

export interface Commit {
  hash: string;
  author: string;
  at: string;
  subject: string;
  files: WorkspaceFile[];
}

export interface Seed {
  log: Log;
  tasks: TaskDetail[];
  stories: Map<string, Story>; // each task's story, for the simulator to go on with
  events: Map<string, LogEvent[]>;
  agents: Agent[];
  knowledge: Map<string, { by: string; at: string; knowledge: string }[]>;
  templates: TeamTemplate[];
  policy: Policy;
  history: PolicyVersion[];
  config: ServerConfig;
  settings: Settings;
  features: FeaturesReply;
  workers: WorkerList;
  workerDetails: Map<string, WorkerDetail>;
  connections: { id: string; host: string; user: string; os: string; madeAt: string }[];
  files: { path: string; change: WorkspaceFile["change"]; now: string; before: string }[];
  commits: Commit[];
}

const minutesAgo = (now: Date, minutes: number) => new Date(now.getTime() - minutes * 60_000);

const PROFILES: Record<string, string> = {
  "tech-lead": "read-only",
  engineer: "developer",
  reviewer: "read-only",
  tester: "developer",
  deployer: "developer",
};

function agentsOf(): Agent[] {
  const agent = (name: string, description: string, prompt: string, color: string, extra: Partial<Agent> = {}): Agent => ({
    name,
    description,
    prompt,
    engine: "claude",
    model: "",
    profile: PROFILES[name],
    coordinates: false,
    team: "",
    template: "software",
    base: "software",
    workers: [],
    color,
    column: "",
    resume: "",
    knowledge: "",
    effective: { profile: PROFILES[name], own: PROFILES[name], overridden: false, missing: false, text: PROFILES[name] },
    teamVersion: "1",
    ...extra,
  });
  return [
    agent(
      "tech-lead",
      "Takes every software task, plans it in small steps, decides who works on it next, and when it is done",
      techLead,
      "orange",
      { coordinates: true },
    ),
    agent("engineer", "Writes and changes code on the task branch, with tests, and commits it", engineer, "blue", {
      knowledge:
        "- Run the tests with `npm test`; the API tests need `make db` first.\n- Money is kept in cents everywhere: never use floats for prices.",
    }),
    agent("reviewer", "Reviews the task branch's changes for bugs, clarity and fit; changes nothing", reviewer, "violet", {
      column: "review",
    }),
    agent("tester", "Runs the tests, adds missing ones, and tries the change for real", tester, "teal", { column: "review" }),
    agent("deployer", "Merges the finished task branch into dev and pushes it (the push asks you first)", deployer, "green", {
      column: "ship",
    }),
  ];
}

// profilesOf reads the profiles of the policy Agentry ships: each profile's
// why and otherwise, and each rule's name, decision and why.
export function profilesOf(text: string): PolicyProfile[] {
  const profiles: PolicyProfile[] = [];
  let rule: PolicyProfile["rules"][number] | null = null;
  for (const line of text.split("\n").map((l) => l.trim())) {
    const header = /^\[profiles\.([\w-]+)\]$/.exec(line);
    if (header) {
      profiles.push({ name: header[1], why: "", otherwise: "ask", rules: [] });
      rule = null;
      continue;
    }
    const profile = profiles[profiles.length - 1];
    if (!profile) continue;
    if (/^\[\[profiles\.[\w-]+\.rules\]\]$/.test(line)) {
      rule = { name: "", decision: "ask", why: "" };
      profile.rules.push(rule);
      continue;
    }
    const field = /^(name|decision|why|otherwise) = "(.*)"$/.exec(line);
    if (!field) continue;
    const [, key, value] = field;
    if (rule && key !== "otherwise") (rule as Record<string, string>)[key] = value;
    else if (key === "why") profile.why = value;
    else if (key === "otherwise") profile.otherwise = value as PolicyProfile["otherwise"];
  }
  return profiles;
}

function policyOf(agents: Agent[], at: Date): { policy: Policy; history: PolicyVersion[] } {
  const profiles = profilesOf(policyText);
  const hash = "3f9a1c07e2b4";
  const users: Policy["users"] = {};
  for (const agent of agents) (users[agent.profile] ??= []).push({ agent: agent.name, followsDefault: false });
  const policy: Policy = {
    path: "~/.agentry/policy.toml",
    hash,
    problem: "",
    defaultProfile: "developer",
    useForEveryone: "",
    profiles,
    text: policyText,
    version: VERSION,
    defaults: profiles.map((p) => ({ name: p.name, state: "current", text: "" })),
    users,
    effective: Object.fromEntries(agents.map((a) => [a.name, a.effective!])),
    missing: {},
  };
  const history: PolicyVersion[] = [
    {
      position: 3,
      at: at.toISOString(),
      actor: { kind: "system", id: "defaults" },
      change: "Made from Agentry's default policy",
      hash,
      canRevert: false,
    },
  ];
  return { policy, history };
}

function tasksOf(log: Log, now: Date) {
  const told: { detail: TaskDetail; events: LogEvent[]; story: Story }[] = [];
  const add = (
    id: string,
    column: TaskDetail["column"],
    title: string,
    description: string,
    beats: Beat[],
    minutes: number,
    extra: Partial<TaskDetail> = {},
  ) => {
    const summary = { ...task(id, column), title, repo: REPO, rank: told.length + 1, ...extra };
    const story = tell(log, summary, description, beats, minutesAgo(now, minutes));
    const detail = story.detail();
    told.push({ detail, events: story.events, story });
    return detail;
  };

  const epic = add(
    "task-5",
    "in-progress",
    "Faster checkout",
    "Make checkout take fewer steps: show the totals with tax up front, and let people export their orders.",
    [],
    300,
    { kind: "epic", stories: 2, storiesDone: 1 },
  );

  add(
    "task-6",
    "done",
    "Show order totals with tax",
    "Show the order total with tax on the cart page, before checkout.",
    [
      {
        kind: "handoff",
        to: "engineer",
        text: "Add the tax to the cart total in `cart/summary.ts`, with a test for a cart with mixed tax rates.",
      },
      {
        kind: "work",
        agent: "engineer",
        plan: "- [x] Read the cart summary\n- [x] Add tax per line\n- [x] Test mixed rates",
        commands: ["npm test -- cart"],
        report: "Totals now include tax per line; 6 new tests pass.",
        tokens: 38_000,
      },
      { kind: "handoff", to: "reviewer", text: "Review the tax change on the task branch." },
      {
        kind: "work",
        agent: "reviewer",
        plan: "",
        commands: ["git diff dev...HEAD", "npm test"],
        report: "Approved: rounding is done once per line, as the spec says.",
        tokens: 21_000,
      },
      { kind: "handoff", to: "deployer", text: "Merge into dev and push." },
      {
        kind: "work",
        agent: "deployer",
        plan: "",
        commands: ["git merge --no-ff agentry/task-6", "git push origin dev"],
        report: "Merged into dev and pushed.",
        tokens: 9_000,
      },
      { kind: "done", summary: "The cart shows the total with tax before checkout. Merged into dev." },
    ],
    280,
    { epic: "task-5", pushed: true },
  );

  add(
    "task-9",
    "done",
    "Add a dark mode switch to Settings",
    "Let people pick light or dark in Settings, and remember it.",
    [
      {
        kind: "handoff",
        to: "engineer",
        text: "Add a theme switch to the Settings page; keep the choice in the account, not the browser.",
      },
      {
        kind: "work",
        agent: "engineer",
        plan: "- [x] Add the setting\n- [x] Switch the page's class\n- [x] Tests",
        commands: ["npm test -- settings"],
        report: "Settings has a Light, Dark and System switch, saved on the account.",
        tokens: 44_000,
      },
      { kind: "handoff", to: "tester", text: "Try the switch in both themes on a phone and a laptop." },
      {
        kind: "work",
        agent: "tester",
        plan: "",
        commands: ["npm run e2e -- theme"],
        report: "Works at 375 and 1440, keyboard included. Added one end-to-end test.",
        tokens: 27_000,
      },
      { kind: "handoff", to: "deployer", text: "Merge into dev and push." },
      { kind: "work", agent: "deployer", plan: "", commands: ["git push origin dev"], report: "Pushed to dev.", tokens: 8_000 },
      { kind: "done", summary: "Settings has a theme switch, tested on a phone and a laptop, and is on dev." },
    ],
    240,
    { pushed: true },
  );

  add(
    "task-10",
    "in-progress",
    "Fix the status box on phones",
    "The order status box overflows on small phones. Make it fit at 320 wide.",
    [
      { kind: "handoff", to: "engineer", text: "Find why the status box overflows at 320 and fix it." },
      {
        kind: "work",
        agent: "engineer",
        plan: "- [x] Reproduce at 320\n- [x] Find the cause",
        commands: ["npm run dev", "npx playwright screenshot --viewport 320x640 /orders/42"],
        report: "The long tracking number doesn't wrap. Two fixes: wrap it, or show the last 8 characters with a copy button.",
        tokens: 31_000,
      },
      {
        kind: "ask",
        question:
          "The tracking number makes the box overflow. Should it wrap onto two lines, or show only its last 8 characters with a copy button?",
        options: ["Wrap it", "Last 8 characters and a copy button"],
        toYou: "The engineer found the cause.",
      },
    ],
    95,
  );

  add(
    "task-11",
    "shipping",
    "Make the task list load fast on refresh",
    "The orders list takes 3 seconds to load after a refresh. Make it load in under half a second.",
    [
      { kind: "handoff", to: "engineer", text: "Profile the orders list and cut its load time; keep the sort order." },
      {
        kind: "work",
        agent: "engineer",
        plan: "- [x] Profile the query\n- [x] Add an index\n- [x] Page the list",
        commands: ["npm run profile -- orders", "npm test"],
        report: "Added an index on (customer, created) and paged the list: 2.9 s down to 180 ms.",
        tokens: 52_000,
      },
      { kind: "handoff", to: "reviewer", text: "Review the index and the paging." },
      {
        kind: "work",
        agent: "reviewer",
        plan: "",
        commands: ["git diff dev...HEAD"],
        report: "Approved. The migration is safe to run online.",
        tokens: 19_000,
      },
      { kind: "handoff", to: "deployer", text: "Merge into dev and push.", toYou: "Reviewed and approved: shipping it." },
      {
        kind: "approval",
        agent: "deployer",
        plan: "- [x] Merge into dev\n- [ ] Push",
        commands: ["git merge --no-ff agentry/task-11"],
        asks: "git push origin dev",
        rule: "pushes ask you",
      },
    ],
    70,
  );

  add(
    "task-12",
    "review",
    "Add retries to the webhook sender",
    "Payment webhooks are lost when the partner's server is down for a moment. Retry them with backoff.",
    [
      { kind: "handoff", to: "engineer", text: "Retry failed webhooks with exponential backoff, at most 5 times, and log each try." },
      {
        kind: "work",
        agent: "engineer",
        plan: "- [x] Add a retry queue\n- [x] Backoff with jitter\n- [x] Tests",
        commands: ["npm test -- webhooks"],
        report: "Webhooks retry 5 times with backoff from 1 s to 30 s. 9 new tests.",
        tokens: 47_000,
      },
      { kind: "handoff", to: "reviewer", text: "Review the retry queue: check a retry can't send a webhook twice." },
      {
        kind: "running",
        agent: "reviewer",
        plan: "- [x] Read the diff\n- [ ] Check duplicates\n- [ ] Run the tests",
        commands: ["git diff dev...HEAD -- src/webhooks"],
      },
    ],
    30,
  );

  add(
    "task-13",
    "todo",
    "Export orders as CSV",
    "Add an Export button to the orders page that downloads the orders shown as CSV.",
    [],
    20,
    { epic: "task-5", after: ["task-6"], parked: "todo", status: "held" },
  );
  add(
    "task-14",
    "backlog",
    "Rate limit the public API",
    "Limit each API key to 60 requests a minute, with a clear error when it is over.",
    [],
    10,
    { parked: "backlog", status: "held" },
  );

  // The epic lists its stories.
  epic.storyList = told.filter((t) => t.detail.epic === epic.id).map((t) => t.detail);
  epic.status = "working";
  return told;
}

const shopFiles = (): Seed["files"] => [
  {
    path: "README.md",
    change: "",
    now: "# Webshop\n\nA small shop: products, a cart, checkout and orders.\n\nRun it with `npm run dev`.\n",
    before: "",
  },
  {
    path: "package.json",
    change: "",
    now: '{\n  "name": "webshop",\n  "private": true,\n  "scripts": { "dev": "vite", "test": "vitest run" }\n}\n',
    before: "",
  },
  {
    path: "src/webhooks/send.ts",
    change: "M",
    now: 'import { backoff } from "./backoff";\n\n// send posts a webhook, and tries again with backoff when the partner is down.\nexport async function send(url: string, body: unknown, tries = 5): Promise<void> {\n  for (let attempt = 1; ; attempt++) {\n    const response = await fetch(url, { method: "POST", body: JSON.stringify(body) });\n    if (response.ok || attempt === tries) return;\n    await backoff(attempt);\n  }\n}\n',
    before:
      '// send posts a webhook.\nexport async function send(url: string, body: unknown): Promise<void> {\n  await fetch(url, { method: "POST", body: JSON.stringify(body) });\n}\n',
  },
  {
    path: "src/webhooks/backoff.ts",
    change: "A",
    now: "// backoff waits longer after each try: 1 s, 2 s, 4 s, up to 30 s, with jitter.\nexport function backoff(attempt: number): Promise<void> {\n  const wait = Math.min(30_000, 1000 * 2 ** (attempt - 1)) * (0.5 + Math.random() / 2);\n  return new Promise((resolve) => setTimeout(resolve, wait));\n}\n",
    before: "",
  },
  {
    path: "src/webhooks/send.test.ts",
    change: "A",
    now: 'import { describe, it } from "vitest";\n\ndescribe("send", () => {\n  it.todo("tries 5 times when the partner is down");\n});\n',
    before: "",
  },
];

export function seed(now = new Date()): Seed {
  const log = new Log();
  const agents = agentsOf();
  const { policy, history } = policyOf(agents, minutesAgo(now, 60 * 24 * 7));
  log.position = 3; // the agents and the policy were made first
  const told = tasksOf(log, now);
  const tasks = told.map((t) => t.detail);
  const workerAbout = {
    host: "build-box",
    os: "Ubuntu 24.04.1 LTS",
    kernel: "6.8.0",
    arch: "amd64",
    cpu: "8-core x86-64",
    cpus: 8,
    memory: 32 * 2 ** 30,
    memoryFree: 21 * 2 ** 30,
    load: [0.42, 0.51, 0.38],
    bootedAt: minutesAgo(now, 60 * 24 * 12).toISOString(),
    disk: 500 * 2 ** 30,
    diskFree: 311 * 2 ** 30,
    time: now.toISOString(),
    zone: "UTC",
    tools: [
      { name: "git", version: "2.43.0", path: "/usr/bin/git" },
      { name: "claude", version: "2.1.301", path: "/usr/local/bin/claude" },
    ],
    agentry: VERSION,
    binary: "/usr/local/bin/agentry",
    folder: "~/.agentry",
    path: ["/usr/local/bin", "/usr/bin", "/bin"],
    running: [],
    worktrees: [],
    repos: [],
  };
  const worker = {
    name: "build-box",
    host: "build-box",
    os: "linux",
    arch: "amd64",
    joinedAt: minutesAgo(now, 60 * 24 * 6).toISOString(),
    online: true,
    since: minutesAgo(now, 60 * 5).toISOString(),
    version: VERSION,
    engines: ["claude 2.1.301"],
    missing: [],
    tasks: [],
  };
  const working = tasks.filter((t) => t.worktree && t.status !== "done").map((t) => t.id);
  const at = (minutes: number) => minutesAgo(now, minutes).toISOString();
  return {
    log,
    tasks,
    stories: new Map(told.map((t) => [t.detail.id, t.story])),
    events: new Map(told.map((t) => [t.detail.id, t.events])),
    agents,
    knowledge: new Map([["engineer", [{ by: "engineer", at: at(200), knowledge: agents[1].knowledge }]]]),
    templates: [
      {
        id: "software",
        team: "Software",
        description: "Builds and changes software: writes the code with tests, reviews it, tries it for real and ships it.",
        features: [],
        agents: agents.map(({ name, description, coordinates, prompt, engine, model, profile, color, column, resume }) => ({
          name,
          description,
          coordinates,
          prompt,
          engine,
          model,
          profile,
          color,
          column,
          resume,
        })),
        userMade: false,
      },
      {
        id: "writing",
        team: "Writing",
        description: "Writes articles, documentation, reports and proposals: researched, drafted for the reader, fact-checked and edited.",
        features: [],
        agents: [
          {
            name: "editor",
            description: "Takes every piece of writing, sets its brief, decides who works on it next, and edits it",
            coordinates: true,
            prompt: "",
            engine: "claude",
            model: "",
            profile: "read-only",
            color: "orange",
            column: "",
            resume: "",
          },
          {
            name: "writer",
            description: "Outlines and drafts the piece for its reader, in plain language, every fact sourced",
            coordinates: false,
            prompt: "",
            engine: "claude",
            model: "",
            profile: "developer",
            color: "blue",
            column: "",
            resume: "",
          },
        ],
        userMade: false,
      },
    ],
    policy,
    history,
    config: {
      defaultRepo: REPO,
      budget: 0,
      maxTurns: 40,
      engines: ["claude"],
      workers: ["local", "build-box"],
      canAddWorkers: true,
      policyProblem: false,
      missingProfiles: {},
    },
    settings: {
      visionariesOnEpicClose: false,
      autoAcceptIdeas: false,
      autoStartIdeas: false,
      autoAcceptRule: "everyoneBacked",
      maxRunningTasks: 3,
      runningTasks: tasks.filter((t) => t.currentAgent).length,
    },
    features: {
      features: [{ name: "ideas", enabled: false, visible: false, why: "Off: no team has the Visionaries." }],
      board: { columns: ["backlog", "todo", "in-progress", "review", "shipping", "done"] },
      unknown: [],
    },
    workers: {
      workers: [worker],
      local: {
        tasks: working,
        machine: { host: "laptop", os: "darwin", arch: "arm64", version: VERSION, engines: ["claude 2.1.301"], missing: [] },
      },
    },
    workerDetails: new Map([
      [
        "build-box",
        {
          worker,
          about: workerAbout,
          aboutError: "",
          agents: [],
          history: [
            { at: worker.since, what: "online" },
            { at: worker.joinedAt, what: "joined" },
          ],
        },
      ],
    ]),
    connections: [{ id: "conn-1", host: "laptop", user: "dev", os: "darwin", madeAt: at(60 * 24 * 3) }],
    files: shopFiles(),
    commits: [
      {
        hash: "4be1d0c",
        author: "engineer",
        at: at(18),
        subject: "feat(webhooks): retry failed sends with backoff",
        files: [
          { path: "src/webhooks/send.ts", change: "M" },
          { path: "src/webhooks/backoff.ts", change: "A" },
        ],
      },
      {
        hash: "91a7f3e",
        author: "engineer",
        at: at(16),
        subject: "test(webhooks): tries 5 times when the partner is down",
        files: [{ path: "src/webhooks/send.test.ts", change: "A" }],
      },
    ],
  };
}
