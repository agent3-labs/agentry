package coordinator

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/storetest"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	watch "gitlab.com/dev.chempa/agentry/internal/watchdog"
	"gitlab.com/dev.chempa/agentry/internal/workers"
)

// You move a task to Done while the engineer works: it is done at once, and
// the coordinator gets exactly one turn to wrap up, whose summary is kept.
// Nothing runs after it.
func TestYouFinishATaskAndTheCoordinatorWrapsUpOnce(t *testing.T) {
	s, _, id := taskWithTheEngineerWorking(t)
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskDone,
		Data: map[string]any{"summary": "Moved.", "note": "Good enough for now.", "moved": true}})

	task := waitForTurn(t, s, id, "turn-3", "tech-lead")
	if !task.IsDone || !task.Turns[1].Stopped {
		t.Fatalf("done %v, the engineer's turn stopped %v", task.IsDone, task.Turns[1].Stopped)
	}
	brief := readBrief(t, task, "turn-3")
	for _, want := range []string{"I moved this task to Done: it is finished as it is. Good enough for now.", "## Wrap up", "it stays done", WrapUpFormat} {
		if !strings.Contains(brief, want) {
			t.Errorf("the wrap-up brief is missing %q", want)
		}
	}
	if strings.Contains(brief, `"action": "assign"`) {
		t.Error("the wrap-up brief offers new work")
	}

	endTurn(t, s, id, "turn-3", "tech-lead", `{"action": "done", "summary": "Half the TODOs are gone; the rest are in src/b.go."}`)
	task = waitUntil(t, s, id, "the wrap-up to end", func(task *tasks.Task) bool { return task.WrapUp == nil })
	if task.Summary != "Half the TODOs are gone; the rest are in src/b.go." {
		t.Errorf("summary %q", task.Summary)
	}
	nothingMoreRuns(t, s, id, 3)
}

// A wrap-up that decides anything but its summary starts nothing: the
// rules refuse it, and the task stays done.
func TestAWrapUpCantStartMoreWork(t *testing.T) {
	s, _, id := taskWithTheEngineerWorking(t)
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Moved.", "moved": true}})
	waitForTurn(t, s, id, "turn-3", "tech-lead")
	endTurn(t, s, id, "turn-3", "tech-lead", `{"action": "assign", "agent": "engineer", "instructions": "Finish src/b.go."}`)

	task := waitUntil(t, s, id, "the wrap-up to end", func(task *tasks.Task) bool { return task.WrapUp == nil })
	if !task.IsDone || task.Summary != "Moved." || !strings.Contains(task.Told.Text, `decided "assign"`) {
		t.Errorf("done %v, summary %q, told %q", task.IsDone, task.Summary, task.Told.Text)
	}
	_, rejection, _ := s.Propose(events.Proposal{Stream: id, Actor: coordinator, Type: events.TaskAssigned,
		Data: map[string]any{"agent": "engineer", "instructions": "Finish src/b.go."}})
	if rejection == nil || rejection.Reason != "the user moved this task to Done; it stays done" {
		t.Errorf("an assignment after your Done: %v", rejection)
	}
	nothingMoreRuns(t, s, id, 3)
}

// A wrap-up turn whose engine crashes still ends: the watchdog watches a
// finished task while its coordinator wraps up, and nothing runs after.
func TestAWrapUpThatCrashesStillEnds(t *testing.T) {
	s, state, id := taskWithTheEngineerWorking(t)
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Moved.", "moved": true}})
	waitForTurn(t, s, id, "turn-3", "tech-lead")

	dead := func(string) bool { return false }
	watch.New(s, state, watch.DefaultLimits, dead, func(string) {}, func(string, string) error { return nil }).Check(time.Now().Add(time.Minute))
	task := waitUntil(t, s, id, "the wrap-up to end", func(task *tasks.Task) bool { return task.WrapUp == nil })
	if !task.IsDone || task.Turns[2].Issue == "" || !strings.Contains(task.Told.Text, "ended without a reply") {
		t.Errorf("done %v, issue %q, told %q", task.IsDone, task.Turns[2].Issue, task.Told.Text)
	}
	nothingMoreRuns(t, s, id, 3)
}

// Moved back to work while its coordinator wraps up, the task stops the
// wrap-up turn, whose late decision changes nothing, and carries on in the
// same worktree.
func TestReopeningStopsTheWrapUp(t *testing.T) {
	s, _, id := taskWithTheEngineerWorking(t)
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Moved.", "moved": true}})
	before := waitForTurn(t, s, id, "turn-3", "tech-lead").Worktree
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskReopened, Data: map[string]any{"text": "Reopen this task. It needs more work."}})

	task := load(t, s, id)
	if task.IsDone || !task.Turns[2].Stopped || task.WrapUp != nil {
		t.Errorf("done %v, the wrap-up stopped %v, wrap-up %+v", task.IsDone, task.Turns[2].Stopped, task.WrapUp)
	}
	if _, rejection, _ := s.Propose(events.Proposal{Stream: id, Actor: worker, Type: events.TurnEnded, Correlation: "turn-3",
		Data: map[string]any{"agent": "tech-lead", "report": `{"action": "done", "summary": "Wrapped."}`}}); rejection == nil {
		t.Error("the stopped wrap-up turn ended with a decision")
	}
	task = waitForTurn(t, s, id, "turn-4", "tech-lead")
	made := 0
	for _, e := range mustStream(t, s, id) {
		if e.Type == events.WorktreeReady {
			made++
		}
	}
	if task.IsDone || made != 1 || *task.Worktree != *before {
		t.Errorf("done %v, worktrees made %d, worktree %+v, was %+v", task.IsDone, made, task.Worktree, before)
	}
}

// Moved from Done to To do once its coordinator has wrapped up, the task
// has nothing new to wrap up: no turn starts.
func TestNoSecondWrapUp(t *testing.T) {
	s, _, id := taskWithTheEngineerWorking(t)
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Moved.", "moved": true}})
	waitForTurn(t, s, id, "turn-3", "tech-lead")
	endTurn(t, s, id, "turn-3", "tech-lead", `{"action": "done", "summary": "Half done."}`)
	waitUntil(t, s, id, "the wrap-up to end", func(task *tasks.Task) bool { return task.WrapUp == nil })

	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskReopened, Data: map[string]any{"text": "Reopen this task and park it."}})
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskHeld, Data: map[string]any{"place": "todo"}})
	if task := load(t, s, id); task.Parked != "todo" || task.WrapUp != nil {
		t.Errorf("parked %q, wrap-up %+v", task.Parked, task.WrapUp)
	}
	nothingMoreRuns(t, s, id, 3)
}

// An epic you close parks its open stories: a story at work wraps up once,
// and a story finished later doesn't report to the closed epic.
func TestClosingAnEpicParksItsStories(t *testing.T) {
	s, repo := twoTeams(t)
	const id = "task-1"
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskCreated, Data: map[string]any{
		"title": "Ship the TODO tool", "repo": repo, "coordinator": "tech-lead", "teamChosen": true, "kind": "epic", "kindChosen": true}})
	waitForTurn(t, s, id, "turn-1", "tech-lead")
	endTurn(t, s, id, "turn-1", "tech-lead", `{"action": "split", "stories": [
		{"title": "Add the TODO store", "brief": "A store."}, {"title": "Add the TODO command", "brief": "A command."}]}`)
	epic := waitUntil(t, s, id, "the split", func(task *tasks.Task) bool { return len(task.Stories) == 2 })
	running, waiting := epic.Stories[0], epic.Stories[1]
	start(t, s, running)
	waitForTurn(t, s, running, "turn-1", "tech-lead")
	waitUntil(t, s, id, "the epic to wait", func(task *tasks.Task) bool { return task.OpenTurn() == nil && task.Handoff == nil })

	// What dragging the epic to Done writes (board_api.go).
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Moved.", "moved": true}})
	propose(t, s, events.Proposal{Stream: running, Actor: you, Type: events.TaskHeld, Data: map[string]any{"place": "backlog"}})

	waitForTurn(t, s, running, "turn-2", "tech-lead")
	endTurn(t, s, running, "turn-2", "tech-lead", `{"action": "done", "summary": "The store is half in."}`)
	waitUntil(t, s, running, "the story's wrap-up to end", func(task *tasks.Task) bool { return task.WrapUp == nil })
	waitForTurn(t, s, id, "turn-2", "tech-lead")
	endTurn(t, s, id, "turn-2", "tech-lead", `{"action": "done", "summary": "Closed early."}`)
	waitUntil(t, s, id, "the epic's wrap-up to end", func(task *tasks.Task) bool { return task.WrapUp == nil })

	propose(t, s, events.Proposal{Stream: waiting, Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Moved.", "moved": true}})
	nothingMoreRuns(t, s, running, 2)
	nothingMoreRuns(t, s, waiting, 0)
	nothingMoreRuns(t, s, id, 2)
	if epic := load(t, s, id); !epic.IsDone || len(epic.StoriesDone) != 0 {
		t.Errorf("the epic: done %v, heard %v", epic.IsDone, epic.StoriesDone)
	}
	all, _ := s.After(0, 10000)
	for _, e := range all {
		if e.Stream == id && e.Type == events.ProposalRejected {
			t.Errorf("refused on the closed epic: %s", e.Text("reason"))
		}
	}
}

// taskWithTheEngineerWorking runs the loop on a task until the engineer's
// turn (turn-2) is open. The task is the store's first, task-1.
func taskWithTheEngineerWorking(t *testing.T) (*store.Store, *current.State, string) {
	const id = "task-1"
	fakeClaudeOnPath(t)
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	repo := gitRepo(t)
	run(t, newLoop(s, state, t.TempDir()), state)

	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskCreated,
		Data: map[string]any{"title": "Remove the TODOs", "repo": repo, "coordinator": "tech-lead"}})
	waitForTurn(t, s, id, "turn-1", "tech-lead")
	endTurn(t, s, id, "turn-1", "tech-lead", `{"action": "assign", "agent": "engineer", "instructions": "Remove every TODO."}`)
	waitForTurn(t, s, id, "turn-2", "engineer")
	return s, state, id
}

// nothingMoreRuns wakes the loop for the task, and checks no turn starts.
func nothingMoreRuns(t *testing.T, s *store.Store, id string, turns int) {
	t.Helper()
	propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskLabeled, Data: map[string]any{"labels": []string{"wake"}}})
	time.Sleep(300 * time.Millisecond)
	if task := load(t, s, id); len(task.Turns) != turns || task.OpenTurn() != nil {
		t.Errorf("%d turns after the wrap-up, want %d", len(task.Turns), turns)
	}
}

func propose(t *testing.T, s *store.Store, p events.Proposal) {
	t.Helper()
	if _, rejection, err := s.Propose(p); rejection != nil || err != nil {
		t.Fatal(p.Type, rejection, err)
	}
}

// A task you moved to Done keeps its port while its coordinator wraps up,
// since reopening it then works in the same worktree; after that it is free.
func TestATaskWrappingUpKeepsItsPort(t *testing.T) {
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	for i, id := range []string{"task-1", "task-2", "task-3"} {
		propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskCreated, Data: map[string]any{"title": id, "repo": "/r", "coordinator": "tech-lead"}})
		propose(t, s, events.Proposal{Stream: id, Actor: worker, Type: events.WorktreeReady, Data: map[string]any{"path": "/w/" + id, "branch": "b", "port": 7901 + i}})
		propose(t, s, events.Proposal{Stream: id, Actor: worker, Type: events.TurnStarted, Correlation: "turn-1", Data: map[string]any{"agent": "tech-lead", "hash": "h"}})
	}
	for _, id := range []string{"task-2", "task-3"} {
		propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Moved.", "moved": true}})
	}
	propose(t, s, events.Proposal{Stream: "task-3", Actor: watchdog, Type: events.TaskWrappedUp, Data: map[string]any{"reason": "no usable decision"}})

	if ports := (&Loop{store: s, state: state}).portsInUse(workers.Local); !slices.Equal(ports, []int{7901, 7902}) {
		t.Errorf("ports in use %v: want the working task's and the one wrapping up", ports)
	}
}

// A task whose state can't be built still holds the port its stream last
// gave it, on that machine only, so it isn't handed out again. The rules
// never let a worktree.moved come first; with no check, it breaks the task.
// The broken tasks' streams then give other ports, by worktree.ready and
// worktree.moved: the last one counts.
func TestABrokenTaskKeepsItsPort(t *testing.T) {
	state := current.New(events.Core())
	s, err := store.Open(filepath.Join(t.TempDir(), "agentry.db"), events.Core(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	created := func(id string) events.Proposal {
		return events.Proposal{Stream: id, Actor: you, Type: events.TaskCreated, Data: map[string]any{"title": id, "repo": "/r", "coordinator": "tech-lead"}}
	}
	for _, p := range []events.Proposal{
		created("task-1"),
		{Stream: "task-1", Actor: worker, Type: events.WorktreeReady, Data: map[string]any{"path": "/w/task-1", "branch": "b", "port": 7901}},
		created("task-2"),
		{Stream: "task-2", Actor: worker, Type: events.WorktreeMoved, Data: map[string]any{"worker": workers.Local, "path": "/w/task-2", "port": 7902}},
		{Stream: "task-2", Actor: worker, Type: events.WorktreeReady, Data: map[string]any{"path": "/w/task-2", "branch": "b", "port": 7912}},
		{Stream: "task-2", Actor: worker, Type: events.WorktreeReady, Data: map[string]any{"path": "/w/task-2", "branch": "b", "port": 7922}},
		created("task-3"),
		{Stream: "task-3", Actor: worker, Type: events.WorktreeMoved, Data: map[string]any{"worker": "laptop", "path": "/w/task-3", "port": 7903}},
		{Stream: "task-3", Actor: worker, Type: events.WorktreeReady, Data: map[string]any{"worker": "laptop", "path": "/w/task-3", "branch": "b", "port": 7913}},
		{Stream: "task-3", Actor: worker, Type: events.WorktreeMoved, Data: map[string]any{"worker": "laptop", "path": "/w/task-3", "port": 7923}},
	} {
		propose(t, s, p)
	}
	if state.Broken("task-2") == nil || state.Broken("task-3") == nil {
		t.Fatal("task-2 and task-3 should be broken")
	}
	loop := &Loop{store: s, state: state}
	if ports := loop.portsInUse(workers.Local); !slices.Equal(ports, []int{7901, 7922}) {
		t.Errorf("ports in use here %v: want task-1's and broken task-2's last", ports)
	}
	if ports := loop.portsInUse("laptop"); !slices.Equal(ports, []int{7923}) {
		t.Errorf("ports in use on the laptop %v: want broken task-3's last", ports)
	}
}

// An archived task is done, so its port is free: it is skipped like a done
// task, not read from its stream as a broken one is.
func TestAnArchivedTaskHoldsNoPort(t *testing.T) {
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	for i, id := range []string{"task-1", "task-2"} {
		propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskCreated, Data: map[string]any{"title": id, "repo": "/r", "coordinator": "tech-lead"}})
		propose(t, s, events.Proposal{Stream: id, Actor: worker, Type: events.WorktreeReady, Data: map[string]any{"path": "/w/" + id, "branch": "b", "port": 7901 + i}})
	}
	propose(t, s, events.Proposal{Stream: "task-2", Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Moved.", "moved": true}})
	storetest.Archive(t, s, state, "task-2")

	if ports := (&Loop{store: s, state: state}).portsInUse(workers.Local); !slices.Equal(ports, []int{7901}) {
		t.Errorf("ports in use %v: want only the working task's", ports)
	}
}

// An archived task is done, so a story after it isn't blocked by it and
// starts; a story after one that isn't done still waits.
func TestAStoryAfterAnArchivedTaskStarts(t *testing.T) {
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	for _, id := range []string{"task-1", "task-2"} {
		propose(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskCreated, Data: map[string]any{"title": id, "repo": "/r", "coordinator": "tech-lead"}})
	}
	propose(t, s, events.Proposal{Stream: "task-1", Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "Shipped.", "moved": true}})
	storetest.Archive(t, s, state, "task-1")

	loop := &Loop{store: s, state: state}
	if loop.blocked(&tasks.Task{After: []string{"task-1"}}) {
		t.Error("a story after an archived task is blocked")
	}
	if !loop.blocked(&tasks.Task{After: []string{"task-1", "task-2"}}) {
		t.Error("a story after a task that isn't done isn't blocked")
	}
}
