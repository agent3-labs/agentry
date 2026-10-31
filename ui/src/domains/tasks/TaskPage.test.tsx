import { describe, expect, test, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import type { TaskDetail } from "@/api/types";
import { task } from "@/api/tasks.fixtures";
import { eraseTask } from "@/api/privacy";
import { dateTime } from "@/lib/time";
import { TaskPage } from "./TaskPage";

// Each path answers with what replies holds, or is still loading, or
// what the server refused in refusals.
const replies = new Map<string, unknown>();
const refusals = new Map<string, { error: string; status: number }>();
vi.mock("@/live/use-resource", () => ({
  useResource: (path: string) => ({
    data: replies.get(path),
    error: refusals.get(path)?.error ?? "",
    status: refusals.get(path)?.status ?? 0,
    position: 0,
    reload: async () => {},
  }),
}));
vi.mock("@/api/privacy", () => ({ eraseTask: vi.fn(), previewRedaction: vi.fn(), redact: vi.fn() }));
// The page's imports reach the shell's install button, which reads
// matchMedia as it loads; jsdom has none.
vi.mock("@/lib/install", () => ({
  watchInstall: () => {},
  installState: () => "unavailable",
  useInstall: () => "unavailable",
  install: async () => {},
}));
vi.mock("@/live/use-events", () => ({
  useEvents: () => ({
    events: [],
    loading: false,
    error: undefined,
    earlier: false,
    loadingEarlier: false,
    loadEarlier: () => {},
    retry: () => {},
  }),
}));

const ARCHIVED_AT = "2026-09-01T10:00:00Z";

function detail(more: Partial<TaskDetail> = {}): TaskDetail {
  return {
    ...task("task-1", "done"),
    status: "done",
    description: "",
    firstTitle: "",
    options: [],
    page: "",
    coordinator: "tech-lead",
    teamChosen: true,
    epicTitle: "",
    storyList: [],
    shared: [],
    movingTo: "",
    summary: "Shipped it.",
    worktree: null,
    trail: null,
    turnList: null,
    tokensUnknown: "",
    approvals: null,
    actions: null,
    ...more,
  };
}

function page(shown: TaskDetail | { error: string; status: number }, tab = "coordinator") {
  replies.clear();
  refusals.clear();
  if ("error" in shown) refusals.set("/api/tasks/task-1", shown);
  else replies.set("/api/tasks/task-1", shown);
  replies.set("/api/agents", []);
  replies.set("/api/tasks", []);
  render(
    <MemoryRouter initialEntries={[`/tasks/task-1?tab=${tab}`]}>
      <Routes>
        <Route path="/tasks/:taskId" element={<TaskPage />} />
      </Routes>
      {/* Another task, opened from inside the app, as the command palette does. */}
      <Link to="/tasks/task-2">Open task-2</Link>
    </MemoryRouter>,
  );
}

const button = (name: string) => screen.queryByRole("button", { name });

describe("TaskPage for an archived task", () => {
  test("marks it archived and offers nothing that writes", () => {
    page(detail({ archived: true, archivedAt: ARCHIVED_AT }));
    expect(screen.getByText(/^Archived /)).toBeTruthy();
    expect(button("Add labels")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Message tech-lead" })).toBeNull();
    expect(screen.getByText("The task is archived: it takes no messages.")).toBeTruthy();
  });

  test("keeps its repo and epic as they are", () => {
    page(detail({ archived: true, archivedAt: ARCHIVED_AT }), "details");
    expect(button("Change")).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Epic" })).toBeNull();
  });

  test("shares no files and takes none away", () => {
    page(detail({ archived: true, archivedAt: ARCHIVED_AT, shared: [{ name: "notes.md", size: 1, by: "you", at: ARCHIVED_AT }] }), "files");
    expect(button("Share files")).toBeNull();
    expect(screen.queryByTitle("Stop sharing it")).toBeNull();
  });

  // Only a done task is archived, and LimitEditor shows a done task's
  // limits without editing them.
  test("shows its tokens and turns, and changes neither", () => {
    page(detail({ archived: true, archivedAt: ARCHIVED_AT, budget: 5000, tokensUsed: 1200, maxTurns: 8 }));
    expect(screen.getByText("Tokens")).toBeTruthy();
    expect(screen.getByText("Turns since your answer")).toBeTruthy();
    expect(button("Change the budget")).toBeNull();
    expect(button("Change the max turns")).toBeNull();
  });
});

describe("TaskPage for a live task", () => {
  test("has no archived mark, and its controls", () => {
    page(detail());
    expect(screen.queryByText(/^Archived /)).toBeNull();
    expect(button("Add labels")).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "Message tech-lead" })).toBeTruthy();
  });

  test("can change its repo and epic", () => {
    page(detail(), "details");
    expect(button("Change")).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Epic" })).toBeTruthy();
  });

  test("can share files and take them away", () => {
    page(detail({ shared: [{ name: "notes.md", size: 1, by: "you", at: ARCHIVED_AT }] }), "files");
    expect(button("Share files")).toBeTruthy();
    expect(screen.getByTitle("Stop sharing it")).toBeTruthy();
  });

  test("can change its budget and max turns while it isn't done", () => {
    page(detail({ status: "working", budget: 5000, tokensUsed: 1200, maxTurns: 8 }));
    expect(button("Change the budget")).toBeTruthy();
    expect(button("Change the max turns")).toBeTruthy();
  });
});

describe("TaskPage's ids", () => {
  const ids = () => screen.getAllByText(/^(task|epic)-\d+$/).map((shown) => shown.textContent);

  test("opens the workspace in a new tab that can't reach this one", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    page(detail({ worktree: { worker: "local", path: "/work/task-1", branch: "agentry/task-1", base: "dev", port: 7901 } }));
    fireEvent.click(screen.getByRole("button", { name: "Workspace" }));
    expect(open).toHaveBeenCalledWith("/tasks/task-1/workspace", "_blank", "noopener");
    open.mockRestore();
  });

  test("shows an epic as epic-N, in the header and its details", () => {
    page(detail({ kind: "epic" }), "details");
    expect(ids()).toEqual(["epic-1", "epic-1"]);
    fireEvent.click(button("Erase this task…")!);
    expect(screen.getByRole("dialog", { name: "Erase epic-1 for good?" })).toBeTruthy();
  });

  test("shows a story as task-N, with its epic as epic-M, linking to task-M", () => {
    page(detail({ epic: "task-9", epicTitle: "Ship v2" }));
    expect(ids()).toEqual(["task-1", "epic-9"]);
    expect(screen.getByRole("link", { name: "epic-9" }).getAttribute("href")).toBe("/tasks/task-9");
  });

  test("names an archived story's epic as epic-M in its details", () => {
    page(detail({ epic: "task-9", archived: true, archivedAt: ARCHIVED_AT }), "details");
    expect(ids()).toEqual(["task-1", "epic-9", "task-1", "epic-9"]);
  });

  test("shows a task on its own as task-N", () => {
    page(detail());
    expect(ids()).toEqual(["task-1"]);
  });
});

const ERASED_AT = "2026-10-20T09:30:00Z";

describe("TaskPage erasing the task", () => {
  test("asks first, saying nothing undoes it and what goes, and erases nothing until you say so", () => {
    page(detail(), "details");
    fireEvent.click(button("Erase this task…")!);
    const question = screen.getByRole("dialog", { name: "Erase task-1 for good?" });
    expect(question.textContent).toContain("Nothing undoes this.");
    expect(question.textContent).toContain("the turn is stopped first");
    expect(question.textContent).toContain("its archive");
    expect(question.textContent).toContain("the shared files no other task names");
    expect(question.textContent).toContain("in its epic and in the task that asked for it");
    expect(eraseTask).not.toHaveBeenCalled();

    fireEvent.click(button("Keep")!);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(eraseTask).not.toHaveBeenCalled();
  });

  test("once erased, shows the task erased with the time, what may be left, and the note on backups", async () => {
    vi.mocked(eraseTask).mockResolvedValue({ id: "task-1", at: ERASED_AT, warnings: ["the shared file abc can't be deleted yet: busy"] });
    page(detail(), "details");
    fireEvent.click(button("Erase this task…")!);
    fireEvent.change(screen.getByLabelText("Why, for the log (optional)"), { target: { value: "asked to" } });
    await act(async () => fireEvent.click(button("Erase for good")!));

    expect(eraseTask).toHaveBeenCalledWith("task-1", "asked to");
    expect(screen.getByRole("heading", { name: `task-1 was erased at ${dateTime(ERASED_AT)}` })).toBeTruthy();
    expect(screen.getByText("the shared file abc can't be deleted yet: busy")).toBeTruthy();
    expect(screen.getByText(/^Backups of agentry.db and the data folder made before/).textContent).toContain(
      `before ${dateTime(ERASED_AT)} still hold it`,
    );
    expect(screen.getByText(/worktree, its local branch and any commits pushed from it/)).toBeTruthy();
    expect(screen.getByText(/session transcripts, live outside Agentry/)).toBeTruthy();
  });

  test("erased from the Details tab while archived, shows the task erased", async () => {
    vi.mocked(eraseTask).mockResolvedValue({ id: "task-1", at: ERASED_AT, warnings: [] });
    page(detail({ archived: true, archivedAt: ARCHIVED_AT }), "details");
    fireEvent.click(button("Erase this task…")!);
    expect(screen.getByRole("dialog", { name: "Erase task-1 for good?" })).toBeTruthy();
    await act(async () => fireEvent.click(button("Erase for good")!));

    expect(eraseTask).toHaveBeenCalledWith("task-1", "");
    expect(screen.getByRole("heading", { name: `task-1 was erased at ${dateTime(ERASED_AT)}` })).toBeTruthy();
  });

  test("the next task opened after an erasure shows its own page, not the erasure", async () => {
    vi.mocked(eraseTask).mockResolvedValue({ id: "task-1", at: ERASED_AT, warnings: [] });
    page(detail(), "details");
    replies.set("/api/tasks/task-2", { ...detail(), ...task("task-2", "done"), status: "done" });
    fireEvent.click(button("Erase this task…")!);
    await act(async () => fireEvent.click(button("Erase for good")!));
    expect(screen.getByRole("heading", { name: `task-1 was erased at ${dateTime(ERASED_AT)}` })).toBeTruthy();

    fireEvent.click(screen.getByRole("link", { name: "Open task-2" }));
    expect(screen.queryByText(/was erased/)).toBeNull();
    expect(screen.getAllByText("Title of task-2").length).toBeGreaterThan(0);
  });

  test("shows a refusal in the server's words, and the task as it was", async () => {
    vi.mocked(eraseTask).mockRejectedValue(new Error("task-1 has stories (task-2); erase or move them out of the epic first"));
    page(detail(), "details");
    fireEvent.click(button("Erase this task…")!);
    await act(async () => fireEvent.click(button("Erase for good")!));
    expect(screen.getByText("It wasn't erased: task-1 has stories (task-2); erase or move them out of the epic first")).toBeTruthy();
    expect(screen.queryByText(/was erased/)).toBeNull();
  });

  test("read again, an erased task shows the server's 410 words, not that there is no such task", () => {
    page({ error: "task-1 was erased on 20 October 2026", status: 410 });
    expect(screen.getByRole("heading", { name: "task-1 was erased on 20 October 2026" })).toBeTruthy();
    expect(screen.queryByText("No task task-1")).toBeNull();
    expect(screen.getByText(/made before then still hold it/)).toBeTruthy();
  });

  test("a task missing for another reason is still no such task", () => {
    page({ error: "no such task task-1", status: 404 });
    expect(screen.getByText("No task task-1")).toBeTruthy();
    expect(screen.queryByText(/was erased/)).toBeNull();
  });
});

describe("TaskPage redacting a text", () => {
  test.each([
    ["a live task", detail()],
    ["an archived task", detail({ archived: true, archivedAt: ARCHIVED_AT })],
  ])("asks about the text marked on the page, in %s", async (_, shown) => {
    page(shown);
    const marked = document.createElement("p");
    marked.textContent = "sk-live-0123456789";
    document.body.append(marked);
    await act(async () => {
      document.getSelection()!.selectAllChildren(marked);
      document.dispatchEvent(new Event("selectionchange"));
    });
    document.getSelection()!.removeAllRanges(); // as the click on the button clears it
    marked.remove();

    fireEvent.click(button("Redact a text")!);
    expect(screen.getByRole("dialog", { name: "Redact a text in task-1" })).toBeTruthy();
    expect((screen.getByLabelText("The text to take out") as HTMLTextAreaElement).value).toBe("sk-live-0123456789");
  });

  test("starts from an empty text when nothing is marked", () => {
    page(detail());
    fireEvent.click(button("Redact a text")!);
    expect((screen.getByLabelText("The text to take out") as HTMLTextAreaElement).value).toBe("");
  });
});

describe("TaskPage tokens", () => {
  const turn = {
    agent: "engineer",
    worker: "local",
    startedAt: "2026-10-24T09:00:00Z",
    lastEventAt: "2026-10-24T09:10:00Z",
    acknowledged: true,
    ended: true,
    issue: "",
    plan: "",
    planFrom: "",
    stopped: false,
  };
  const split = { input: 4, cacheWrite: 12800, cacheRead: 40000, output: 370 };

  test("shows each turn's split stacked under its total, and a turn with none as before", () => {
    page(
      detail({
        tokensUsed: 54174,
        turnList: [
          { ...turn, id: "turn-1", tokens: 53174, split },
          { ...turn, id: "turn-2", tokens: 1000 },
        ],
      }),
      "details",
    );
    const [first, second] = ["turn-1", "turn-2"].map((id) => screen.getByText(id).closest("tr")!);
    expect(first.textContent).toContain("53.2kin4cache write12.8kcache read40kout370");
    const parts = [...first.querySelectorAll("dt")].map((dt) => [dt.textContent, dt.nextElementSibling?.textContent]);
    expect(parts).toEqual([
      ["in", "4"],
      ["cache write", "12.8k"],
      ["cache read", "40k"],
      ["out", "370"],
    ]);
    expect(second.textContent).toContain("1k");
    expect(second.textContent).not.toContain("cache");
    expect(second.querySelector("dl")).toBeNull();
  });

  test("shows each turn's unknown-tokens reason in full, in its own row", () => {
    const long = "no usage in the transcript with a time Agentry can read";
    page(
      detail({
        turnList: [
          { ...turn, id: "turn-1", tokens: 0, tokensUnknown: long },
          { ...turn, id: "turn-2", tokens: 0, tokensUnknown: "transcript not saved" },
        ],
      }),
      "details",
    );
    const reason = screen.getByText(long);
    expect(reason.closest("tr")!.textContent).toContain("turn-1");
    expect(screen.getByText("transcript not saved").closest("tr")!.textContent).toContain("turn-2");
  });

  test("shows under its tokens whether each turn resumed or started fresh, and nothing for a turn that didn't say", () => {
    page(
      detail({
        turnList: [
          { ...turn, id: "turn-1", tokens: 53174, split, freshBecause: "context too big (182k tokens)" },
          { ...turn, id: "turn-2", tokens: 1000, fromSession: "4f1c-9a2e" },
          { ...turn, id: "turn-3", tokens: 1000 },
        ],
      }),
      "details",
    );
    const [first, second, third] = ["turn-1", "turn-2", "turn-3"].map((id) => screen.getByText(id).closest("tr")!);
    expect(first.textContent).toContain("out370fresh: context too big (182k tokens)");
    expect(first.textContent).not.toContain("resumed");
    expect(within(second).getByText("resumed").title).toBe("4f1c-9a2e");
    expect(second.textContent).not.toContain("fresh");
    expect(third.textContent).not.toMatch(/resumed|fresh/);
  });

  test("says which story a turn's session came from, and whether it forked it", () => {
    page(
      detail({
        turnList: [
          { ...turn, id: "turn-1", tokens: 1000, fromSession: "4f1c-9a2e" },
          { ...turn, id: "turn-2", tokens: 1000, fromSession: "4f1c-9a2e", forked: true },
          { ...turn, id: "turn-3", tokens: 1000, fromSession: "4f1c-9a2e", sessionFrom: "task-2" },
          { ...turn, id: "turn-4", tokens: 1000, fromSession: "4f1c-9a2e", sessionFrom: "task-2", forked: true },
        ],
      }),
      "details",
    );
    const rows = ["turn-1", "turn-2", "turn-3", "turn-4"].map((id) => screen.getByText(id).closest("tr")!);
    const labels = rows.map((row) => within(row).getByTitle("4f1c-9a2e"));
    expect(labels.map((label) => label.textContent)).toEqual(["resumed", "forked", "resumed from task-2", "forked from task-2"]);
    expect(within(rows[0]).queryByRole("link")).toBeNull();
    expect(within(rows[3]).getByRole("link", { name: "task-2" }).getAttribute("href")).toBe("/tasks/task-2");
  });

  test("shows the task's split and what isn't split", () => {
    page(detail({ tokensUsed: 54174, tokensSplit: split, tokensUnsplit: 1000 }));
    expect(screen.getByText("in 4 · cache write 12.8k · cache read 40k · out 370 · 1k not split")).toBeTruthy();
  });

  test("shows no split for a task with none", () => {
    page(detail({ tokensUsed: 1000 }));
    expect(screen.queryByText(/cache read/)).toBeNull();
  });
});
