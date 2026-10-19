package rules

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/connections"
	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/ideas"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/workers"
)

var (
	you         = events.Actor{Kind: events.Human, ID: "you"}
	worker      = events.Actor{Kind: events.Worker, ID: "local"}
	policy      = events.Actor{Kind: events.Policy, ID: "rules"}
	agentry     = events.Actor{Kind: events.System, ID: "watchdog"}
	coordinator = events.Actor{Kind: events.Agent, ID: "tech-lead"}
	engineer    = events.Actor{Kind: events.Agent, ID: "engineer"}
)

// step is one proposal and whether it should be accepted.
type step struct {
	actor       events.Actor
	kind        events.Type
	correlation string
	data        map[string]any
	accepted    bool
}

// withIdeas is the catalogue serve opens the log with.
var withIdeas = func() *events.Catalogue {
	catalogue, err := events.NewCatalogue(ideas.Registration())
	if err != nil {
		panic(err)
	}
	return catalogue
}()

// openSeeded is a store with the rules and the five default agents. It
// wires the state as storetest.Open does (which imports rules, so it can't
// be used here), the replay check included.
func openSeeded(t *testing.T) *store.Store {
	t.Helper()
	s, _ := openSeededState(t)
	return s
}

// openSeededState is openSeeded with the state the rules read, for a test
// that changes it by hand, as archiving a task before the state can.
func openSeededState(t *testing.T) (*store.Store, *current.State) {
	t.Helper()
	state := current.New(withIdeas, ideas.Streams())
	s, err := store.Open(filepath.Join(t.TempDir(), "agentry.db"), withIdeas, state, func(p events.Proposal) *events.Rejection { return Check(p, state, withIdeas) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	t.Cleanup(func() {
		if err := current.Equal(s, state); err != nil {
			t.Errorf("the state differs from a replay of the log: %v", err)
		}
	})
	for _, p := range agents.SeedProposals(nil) {
		if _, rejection, err := s.Propose(p); rejection != nil || err != nil {
			t.Fatalf("seeding %s: %v %v", p.Text("name"), rejection, err)
		}
	}
	return s, state
}

func run(t *testing.T, steps []step) *tasks.Task {
	t.Helper()
	s := openSeeded(t)
	for i, st := range steps {
		p := events.Proposal{Stream: "task-1", Actor: st.actor, Type: st.kind, Correlation: st.correlation, Data: st.data}
		_, rejection, err := s.Propose(p)
		if err != nil {
			t.Fatal(err)
		}
		if (rejection == nil) != st.accepted {
			t.Errorf("step %d %s by %s: accepted=%v (%v), want %v", i+1, st.kind, st.actor, rejection == nil, rejection, st.accepted)
		}
	}
	stream, _ := s.Stream("task-1")
	return tasks.FromEvents(stream)
}

var (
	created  = step{you, events.TaskCreated, "", map[string]any{"title": "Remove the TODOs", "repo": "/src/app", "coordinator": "tech-lead"}, true}
	worktree = step{worker, events.WorktreeReady, "", map[string]any{"path": "/w/task-1", "branch": "agentry/task-1", "port": 7901}, true}
)

func turnStarted(id, agent string) step {
	return step{worker, events.TurnStarted, id, map[string]any{"agent": agent, "hash": "h-" + id}, true}
}

func turnEnded(id, agent, report string) step {
	return step{worker, events.TurnEnded, id, map[string]any{"agent": agent, "report": report}, true}
}

func TestATaskGoesRoundTheCoordinator(t *testing.T) {
	task := run(t, []step{
		created,
		{you, events.TaskCreated, "", created.data, false},                                               // only once
		{worker, events.TurnStarted, "turn-1", map[string]any{"agent": "tech-lead", "hash": "h"}, false}, // no worktree yet
		worktree,
		turnStarted("turn-1", "tech-lead"),
		{worker, events.PromptAcknowledged, "turn-1", map[string]any{"hash": "wrong"}, false},
		{worker, events.PromptAcknowledged, "turn-1", map[string]any{"hash": "h-turn-1"}, true},
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "remove them"}, false}, // still in its turn
		turnEnded("turn-1", "tech-lead", `{"action":"assign"}`),
		{engineer, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "x"}, false},     // only the coordinator assigns
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "tech-lead", "instructions": "x"}, false}, // not to itself
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "designer", "instructions": "x"}, false},  // no such agent
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "remove them"}, true},
		{worker, events.TurnStarted, "turn-5", map[string]any{"agent": "engineer", "hash": "h"}, false}, // wrong turn number
		turnStarted("turn-2", "engineer"),
		{coordinator, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "ls"}, false}, // not the working agent
		{engineer, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "ls"}, true},
		{policy, events.ActionApproved, "toolu_1", map[string]any{"reason": "stays inside the worktree"}, true},
		{policy, events.ActionDenied, "toolu_1", map[string]any{"reason": "x"}, false}, // decided once
		{worker, events.ActionResult, "toolu_1", nil, true},
		turnEnded("turn-2", "engineer", "Removed 3 TODOs."),
		turnStarted("turn-3", "tech-lead"),
		turnEnded("turn-3", "tech-lead", `{"action":"done"}`),
		{coordinator, events.TaskDone, "", map[string]any{"summary": "TODOs removed"}, true},
		{agentry, events.IssueRaised, "", map[string]any{"reason": "late"}, false}, // nothing after done
	})

	if task.Status() != tasks.Done || len(task.Turns) != 3 || !task.Turns[0].Acknowledged {
		t.Errorf("status %s, %d turns, first acknowledged %v", task.Status(), len(task.Turns), task.Turns[0].Acknowledged)
	}
	want := []tasks.Step{
		{From: "you", To: "tech-lead", Text: "Remove the TODOs"},
		{From: "tech-lead", To: "engineer", Text: "remove them"},
		{From: "engineer", To: "tech-lead", Text: "Removed 3 TODOs."},
		{From: "tech-lead", To: "you", Text: "TODOs removed"},
	}
	if len(task.Trail) != len(want) {
		t.Fatalf("trail: %+v", task.Trail)
	}
	for i, w := range want {
		if got := task.Trail[i]; got.From != w.From || got.To != w.To || got.Text != w.Text {
			t.Errorf("trail %d: got %s → %s %q, want %s → %s %q", i, got.From, got.To, got.Text, w.From, w.To, w.Text)
		}
	}
}

func TestOnlyAskedActionsAndQuestionsNeedYou(t *testing.T) {
	steps := []step{
		created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "assign"),
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"}, true},
		turnStarted("turn-2", "engineer"),
		{engineer, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "git push"}, true},
	}
	if task := run(t, steps); task.Status() != tasks.Working {
		t.Errorf("a requested action alone should not need you (proof 05), got %s", task.Status())
	}

	steps = append(steps, step{policy, events.ActionAsked, "toolu_1", map[string]any{"reason": "may reach outside the worktree"}, true})
	if task := run(t, steps); task.Status() != tasks.NeedsYou || len(task.WaitingForYou()) != 1 {
		t.Errorf("an asked action should need you, got %s", task.Status())
	}

	steps = append(steps,
		step{you, events.ActionApproved, "toolu_1", map[string]any{"reason": "you approved"}, true},
		step{agentry, events.IssueRaised, "", map[string]any{"reason": "the agent's process exited"}, true},
		step{agentry, events.TaskNeedsYou, "", map[string]any{"question": "too many turns"}, true},
		step{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "again"}, false},
		step{you, events.TaskAnswered, "", map[string]any{"answer": "carry on"}, true},
		step{you, events.TaskAnswered, "", map[string]any{"answer": "twice"}, false},
	)
	task := run(t, steps)
	if task.Status() != tasks.Working || task.Turns[1].Issue != "the agent's process exited" || task.OpenTurn() != nil {
		t.Errorf("status %s, issue %q, open turn %v", task.Status(), task.Turns[1].Issue, task.OpenTurn())
	}
}

// A usage limit is said by the machine running the open turn's agent. It
// isn't an issue: the turn stays open, and the count of issues is as it was.
func TestAUsageLimitKeepsTheTurnOpenAndIsNoIssue(t *testing.T) {
	limited := func(agent string) map[string]any {
		return map[string]any{"agent": agent, "error": "rate_limit", "details": "You've hit your session limit · resets 3am (UTC)", "resetsAt": "2026-10-20T03:00:00Z"}
	}
	task := run(t, []step{
		created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "assign"),
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"}, true},
		turnStarted("turn-2", "engineer"),
		{agentry, events.IssueRaised, "", map[string]any{"reason": "the agent's process exited"}, true},
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "again"}, true},
		{worker, events.TurnLimited, "turn-2", limited("engineer"), false}, // not the open turn
		turnStarted("turn-3", "engineer"),
		{worker, events.TurnLimited, "turn-3", limited("tech-lead"), false},  // not its agent
		{engineer, events.TurnLimited, "turn-3", limited("engineer"), false}, // the machine says it, not the agent
		{worker, events.TurnLimited, "turn-3", limited("engineer"), true},
	})
	turn := task.OpenTurn()
	if turn == nil || turn.ID != "turn-3" || turn.Limited == "" || !turn.ResetsAt.Equal(time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("the limited turn should stay open with its reset time: %+v", turn)
	}
	if task.IssuesInARow != 1 || task.Status() != tasks.Working {
		t.Errorf("issues in a row %d, status %s: a usage limit is no issue", task.IssuesInARow, task.Status())
	}
}

// A limited turn resumes, said by its machine or by Agentry at the reset,
// only while it is the open turn, its agent's, and limited. It stays open,
// and the count of issues is as it was.
func TestALimitedTurnResumes(t *testing.T) {
	limited := map[string]any{"agent": "engineer", "error": "rate_limit", "details": "You've hit your session limit", "resetsAt": ""}
	resumed := func(agent, reason string) map[string]any { return map[string]any{"agent": agent, "reason": reason} }
	task := run(t, []step{
		created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "assign"),
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"}, true},
		turnStarted("turn-2", "engineer"),
		{worker, events.TurnResumed, "turn-2", resumed("engineer", "engine"), false}, // not limited
		{worker, events.TurnLimited, "turn-2", limited, true},
		{worker, events.TurnResumed, "turn-1", resumed("engineer", "engine"), false},   // not the open turn
		{worker, events.TurnResumed, "turn-2", resumed("tech-lead", "engine"), false},  // not its agent
		{engineer, events.TurnResumed, "turn-2", resumed("engineer", "engine"), false}, // the agent can't say it
		{agentry, events.TurnResumed, "turn-2", resumed("engineer", "reset"), true},
		{worker, events.TurnResumed, "turn-2", resumed("engineer", "engine"), false}, // no longer limited
		{worker, events.TurnLimited, "turn-2", limited, true},
		{worker, events.TurnResumed, "turn-2", resumed("engineer", "engine"), true},
	})
	turn := task.OpenTurn()
	if turn == nil || turn.ID != "turn-2" || turn.Limited != "" || turn.Resumes != 2 {
		t.Errorf("the turn should be open, no longer limited, resumed twice: %+v", turn)
	}
	if task.IssuesInARow != 0 || task.Status() != tasks.Working {
		t.Errorf("issues in a row %d, status %s", task.IssuesInARow, task.Status())
	}
}

func TestEveryTaskEventHasARule(t *testing.T) {
	for _, kind := range []events.Type{events.WorktreeReady, events.TaskAssigned, events.TurnStarted,
		events.PromptAcknowledged, events.SessionStarted, events.ActionRequested, events.ActionAsked,
		events.ActionApproved, events.ActionDenied, events.ActionHeld, events.ActionResult, events.AgentWaiting, events.TurnEnded,
		events.TurnLimited, events.TurnResumed, events.IssueRaised, events.TaskNeedsYou, events.TaskAnswered, events.TaskDone} {
		if taskRules[kind] == nil {
			t.Errorf("%s has no rule", kind)
		}
	}
}

// An engineer's turn whose git push was asked about: toolu_1.
var askedPush = []step{
	created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "assign"),
	{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"}, true},
	turnStarted("turn-2", "engineer"),
	{engineer, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "git push"}, true},
	{policy, events.ActionAsked, "toolu_1", map[string]any{"reason": "may reach outside the worktree"}, true},
}

func TestOnlyYouDecideAHeldAction(t *testing.T) {
	approve := func(actor events.Actor, id, pass string, accepted bool) step {
		data := map[string]any{"reason": "approved"}
		if pass != "" {
			data["pass"] = pass
		}
		return step{actor, events.ActionApproved, id, data, accepted}
	}
	request := func(id, command string) step {
		return step{engineer, events.ActionRequested, id, map[string]any{"tool": "Bash", "command": command}, true}
	}
	task := run(t, append(append([]step{}, askedPush...),
		step{you, events.ActionHeld, "toolu_1", nil, false}, // only the policy holds one
		step{policy, events.ActionHeld, "toolu_1", nil, true},
		step{policy, events.ActionHeld, "toolu_1", nil, false}, // held once
		approve(policy, "toolu_1", "", false),                  // no hook waits: only you decide it
		request("toolu_2", "ls"),
		step{policy, events.ActionHeld, "toolu_2", nil, false}, // never asked
		approve(policy, "toolu_2", "toolu_1", false),           // not approved yet
		approve(policy, "toolu_2", "", true),
		turnEnded("turn-2", "engineer", "Pushed? No, it was held."),
		approve(you, "toolu_1", "", true),
		turnStarted("turn-3", "tech-lead"), turnEnded("turn-3", "tech-lead", "assign"),
		step{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "push again"}, true},
		turnStarted("turn-4", "engineer"),
		request("toolu_3", "git push --force"),
		approve(policy, "toolu_3", "toolu_1", false), // another command
		approve(policy, "toolu_3", "toolu_2", false), // not a held action
		request("toolu_4", "git push"),
		approve(policy, "toolu_4", "toolu_1", true),
		request("toolu_5", "git push"),
		approve(policy, "toolu_5", "toolu_1", false), // its pass is used
	))
	if held := task.Action("toolu_1"); !held.Held || held.Decision != tasks.Approved || !held.PassUsed {
		t.Errorf("toolu_1: %+v, want held, approved, its pass used", held)
	}
}

// A pass works for the agent that asked, in its first turn after your
// approval, and in no other.
func TestAPassFitsItsAgentAndTurn(t *testing.T) {
	reviewer := events.Actor{Kind: events.Agent, ID: "reviewer"}
	request := func(actor events.Actor, id string) step {
		return step{actor, events.ActionRequested, id, map[string]any{"tool": "Bash", "command": "git push"}, true}
	}
	usePass := func(id string, accepted bool) step {
		return step{policy, events.ActionApproved, id, map[string]any{"reason": "approved", "pass": "toolu_1"}, accepted}
	}
	handTo := func(agent, decide, next string) []step {
		return []step{turnStarted(decide, "tech-lead"), turnEnded(decide, "tech-lead", "assign"),
			{coordinator, events.TaskAssigned, "", map[string]any{"agent": agent, "instructions": "go"}, true},
			turnStarted(next, agent)}
	}
	log := append(append([]step{}, askedPush...),
		step{policy, events.ActionHeld, "toolu_1", nil, true},
		step{you, events.ActionApproved, "toolu_1", map[string]any{"reason": "late"}, true},
		request(engineer, "toolu_2"),
		usePass("toolu_2", false), // the turn it was asked in, not the next one
		turnEnded("turn-2", "engineer", "held"))
	log = append(log, handTo("reviewer", "turn-3", "turn-4")...)
	log = append(log, request(reviewer, "toolu_3"), usePass("toolu_3", false), // another agent
		turnEnded("turn-4", "reviewer", "reviewed"))
	log = append(log, handTo("engineer", "turn-5", "turn-6")...)
	log = append(log, turnEnded("turn-6", "engineer", "didn't push"))
	log = append(log, handTo("engineer", "turn-7", "turn-8")...)
	task := run(t, append(log, request(engineer, "toolu_4"), usePass("toolu_4", false))) // turn-6 was its turn
	if pass := task.Action("toolu_1"); pass.PassTurn != "turn-6" || pass.PassUsed {
		t.Errorf("toolu_1: %+v, want its pass for turn-6, unused", pass)
	}
}

// When the coordinator itself asked, its next turn hears your approval and
// its pass works there.
func TestACoordinatorsPassWorksInItsNextTurn(t *testing.T) {
	push := func(id string) step {
		return step{coordinator, events.ActionRequested, id, map[string]any{"tool": "Bash", "command": "git push"}, true}
	}
	task := run(t, []step{created, worktree, turnStarted("turn-1", "tech-lead"),
		push("toolu_1"),
		{policy, events.ActionAsked, "toolu_1", map[string]any{"reason": "may reach outside the worktree"}, true},
		{policy, events.ActionHeld, "toolu_1", nil, true},
		turnEnded("turn-1", "tech-lead", "held"),
		{you, events.ActionApproved, "toolu_1", map[string]any{"reason": "late"}, true},
		turnStarted("turn-2", "tech-lead"),
		push("toolu_2"),
		{policy, events.ActionApproved, "toolu_2", map[string]any{"reason": "approved", "pass": "toolu_1"}, true},
	})
	if pass := task.Action("toolu_1"); pass.PassTurn != "turn-2" || !pass.PassUsed {
		t.Errorf("toolu_1: %+v, want its pass used in turn-2", pass)
	}
	handBack := slices.IndexFunc(task.Trail, func(s tasks.Step) bool { return strings.Contains(s.Text, "Run it again yourself in this turn") })
	if handBack < 0 || !strings.HasSuffix(task.Trail[handBack].Text, "in this turn only.") {
		t.Errorf("the hand-back should tell the coordinator to run it in this turn: %+v", task.Trail)
	}
}

func TestAHeldActionWaitsWhileTheTaskIsParkedOrDone(t *testing.T) {
	for name, move := range map[string]step{
		"to do": {you, events.TaskHeld, "", map[string]any{"place": "todo"}, true},
		"done":  {you, events.TaskDone, "", map[string]any{"summary": "Moved.", "moved": true}, true},
	} {
		t.Run(name, func(t *testing.T) {
			task := run(t, append(append([]step{}, askedPush...),
				step{policy, events.ActionHeld, "toolu_1", nil, true},
				move,
				step{you, events.ActionApproved, "toolu_1", map[string]any{"reason": "late"}, false},
				step{you, events.ActionDenied, "toolu_1", map[string]any{"reason": "late"}, false},
			))
			if len(task.WaitingForYou()) != 0 {
				t.Errorf("waiting for you: %+v, want none while the task is %s", task.WaitingForYou(), name)
			}
		})
	}
}

func TestAgentRules(t *testing.T) {
	s := openSeeded(t)
	agent := func(name, engine, prompt string, coordinates bool) agents.Agent {
		return agents.Agent{Name: name, Engine: engine, Prompt: prompt, Coordinates: coordinates}
	}
	cases := []struct {
		name     string
		proposal events.Proposal
		accepted bool
	}{
		{"a new agent", agent("designer", "codex", "Design the page.", false).Proposal(you, false), true},
		{"the same name twice", agent("designer", "claude", "Again.", false).Proposal(you, false), false},
		{"a name that can't be a file name", agent("Data Scientist", "claude", "x", false).Proposal(you, false), false},
		{"an unknown engine", agent("writer", "gpt-cli", "x", false).Proposal(you, false), false},
		{"no prompt", agent("writer", "claude", "  ", false).Proposal(you, false), false},
		{"change an agent", agent("engineer", "codex", "Only Go.", false).Proposal(you, true), true},
		{"change one that doesn't exist", agent("ghost", "claude", "x", false).Proposal(you, true), false},
		{"leave no coordinator", agent("tech-lead", "claude", "x", false).Proposal(you, true), false},
		{"a worker that doesn't exist", agents.Agent{Name: "tester", Engine: "claude", Prompt: "x", Workers: []string{"build-box"}}.Proposal(you, true), false},
		{"no workers: runs on this machine", agents.Agent{Name: "tester", Engine: "claude", Prompt: "x", Workers: nil}.Proposal(you, true), true},
		{"an agent keeps its own notes", agent("engineer", "", "", false).Learned(engineer, "Use gofmt."), true},
		{"not another's", agent("reviewer", "", "", false).Learned(engineer, "x"), false},
		{"you can edit anyone's", agent("reviewer", "", "", false).Learned(you, "Be strict."), true},
		{"a patch on what it knows", agent("engineer", "", "", false).Patching(engineer, agents.Diff("Use gofmt.", "Use gofmt -s.")), true},
		{"a patch on a version since replaced", agent("engineer", "", "", false).Patching(engineer, agents.Diff("Use gofmt.", "Run go vet.\nUse gofmt.")), false},
		{"a patch keeping more than there is", agent("engineer", "", "", false).Patching(engineer, agents.Patch{Head: 10, Tail: 10, SHA256: "x"}), false},
		{"a patch with another text's hash", agent("engineer", "", "", false).Patching(engineer, agents.Patch{Head: 13, Middle: "!", SHA256: agents.Diff("", "x").SHA256}), false},
		{"a patch whose middle isn't UTF-8", agent("engineer", "", "", false).Patching(engineer, agents.Diff("Use gofmt -s.", "Use gofmt -s.\xff")), false},
		{"a patch with no head", events.Proposal{Stream: events.AgentsStream, Actor: engineer, Type: events.AgentLearned,
			Data: map[string]any{"name": "engineer", "tail": 0, "middle": "x", "sha256": agents.Diff("", "x").SHA256}}, false},
		{"a patch for an agent that doesn't exist", agent("ghost", "", "", false).Patching(you, agents.Diff("", "x")), false},
		{"a full copy over any version", agent("engineer", "", "", false).Learned(engineer, "Use gofmt only."), true},
		{"remove an agent", agent("designer", "codex", "x", false).Removal(you), true},
		{"remove it twice", agent("designer", "codex", "x", false).Removal(you), false},
		{"remove the only coordinator", agent("tech-lead", "claude", "x", true).Removal(you), false},
		{"a second team, with its coordinator", agents.Agent{Name: "editor", Engine: "claude", Prompt: "Edit.", Coordinates: true, Team: "Writing"}.Proposal(you, false), true},
		{"and its writer", agents.Agent{Name: "writer", Engine: "claude", Prompt: "Write.", Team: "Writing"}.Proposal(you, false), true},
		{"its coordinator can't stop coordinating", agents.Agent{Name: "editor", Engine: "claude", Prompt: "Edit.", Team: "Writing"}.Proposal(you, true), false},
		{"or go while the team has agents", agents.Agent{Name: "editor"}.Removal(you), false},
		{"a coordinator hands work only to its own team", events.Proposal{Stream: "task-1", Actor: events.Actor{Kind: events.Agent, ID: "tech-lead"},
			Type: events.TaskAssigned, Data: map[string]any{"agent": "writer", "instructions": "x"}}, false},
		{"an agent makes a task only for another, as a request", events.Proposal{Stream: "task-1", Actor: events.Actor{Kind: events.Agent, ID: "tech-lead"},
			Type: events.TaskCreated, Data: map[string]any{"title": "x", "repo": "/r", "coordinator": "tech-lead"}}, false},
		{"a task for a coordinator that isn't one", events.Proposal{Stream: "task-1", Actor: you, Type: events.TaskCreated,
			Data: map[string]any{"title": "x", "repo": "/r", "coordinator": "engineer"}}, false},
	}
	for _, c := range cases {
		_, rejection, err := s.Propose(c.proposal)
		if err != nil {
			t.Fatal(err)
		}
		if (rejection == nil) != c.accepted {
			t.Errorf("%s: accepted=%v (%v), want %v", c.name, rejection == nil, rejection, c.accepted)
		}
	}
}

// The coordinator (or you) may give the task a better title, once it has
// read it; the first title is kept.
func TestTheTaskCanBeRenamed(t *testing.T) {
	task := run(t, []step{
		created,
		{engineer, events.TaskRenamed, "", map[string]any{"title": "Something else"}, false},      // only the coordinator
		{coordinator, events.TaskRenamed, "", map[string]any{"title": "Remove the TODOs"}, false}, // the same title
		{coordinator, events.TaskRenamed, "", map[string]any{"title": "Remove the TODO comments in src/"}, true},
		{you, events.TaskRenamed, "", map[string]any{"title": "Clean up src/"}, true},
	})
	if task.Title != "Clean up src/" || task.FirstTitle != "Remove the TODOs" {
		t.Errorf("title %q, first title %q", task.Title, task.FirstTitle)
	}
}

// A budget and a limit of turns are 0 or above, 0 for no limit.
func TestBudgetsAndLimitsOfTurns(t *testing.T) {
	task := run(t, []step{
		{you, events.TaskCreated, "", map[string]any{"title": "x", "repo": "/r", "coordinator": "tech-lead", "maxTurns": -1}, false},
		{you, events.TaskCreated, "", map[string]any{"title": "x", "repo": "/r", "coordinator": "tech-lead", "budget": -1}, false},
		{you, events.TaskCreated, "", map[string]any{"title": "x", "repo": "/r", "coordinator": "tech-lead", "budget": 0, "maxTurns": 50}, true},
		{you, events.TaskTurnsLimited, "", map[string]any{"maxTurns": -3}, false},
		{coordinator, events.TaskTurnsLimited, "", map[string]any{"maxTurns": 100}, false}, // only you
		{you, events.TaskTurnsLimited, "", map[string]any{"maxTurns": 0}, true},
		{you, events.TaskBudgeted, "", map[string]any{"budget": -1}, false},
		{you, events.TaskBudgeted, "", map[string]any{"budget": 2_000_000}, true},
		{you, events.TaskBudgeted, "", map[string]any{"budget": 0}, true},
	})
	if task.MaxTurns != 0 || task.Budget != 0 {
		t.Errorf("max turns %d, budget %d", task.MaxTurns, task.Budget)
	}
}

// Directions from the board are noted; a question takes an answer instead.
func TestDirectionsWaitForNoQuestion(t *testing.T) {
	task := run(t, []step{
		created,
		{you, events.TaskDirected, "", map[string]any{"text": "Hand this task to reviewer next."}, true},
		{coordinator, events.TaskDirected, "", map[string]any{"text": "x"}, false}, // only you
		worktree,
		turnStarted("turn-1", "tech-lead"),
		turnEnded("turn-1", "tech-lead", `{"action":"ask"}`),
		{coordinator, events.TaskNeedsYou, "", map[string]any{"question": "Which branch?"}, true},
		{you, events.TaskDirected, "", map[string]any{"text": "Wrap it up."}, false},
	})
	if steps := task.Trail; steps[1].Text != "Hand this task to reviewer next." || steps[1].From != "you" {
		t.Errorf("trail: %+v", steps)
	}
}

// Your moves on the board stand: the task is done or parked at once, the
// turn in progress stops, and the coordinator only wraps up, in one turn.
func TestYourMoveToDoneStands(t *testing.T) {
	s := openSeeded(t)
	proposeAll(t, s, []step{
		created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "assign"),
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"}, true},
		turnStarted("turn-2", "engineer"),
		{you, events.TaskDone, "", map[string]any{"summary": "Moved.", "moved": true}, true}, // the engineer's turn stops
		turnStarted("turn-3", "tech-lead"),                                                   // the coordinator's wrap-up
		turnEnded("turn-3", "tech-lead", `{"action":"assign"}`),
	})
	const stays = "the user moved this task to Done; it stays done"
	refusedWith(t, s, coordinator, events.TaskAssigned, map[string]any{"agent": "engineer", "instructions": "more"}, stays)
	refusedWith(t, s, coordinator, events.TaskNeedsYou, map[string]any{"question": "Reopen it?"}, stays)
	refusedWith(t, s, coordinator, events.TaskDone, map[string]any{"summary": "again"}, stays)
	proposeAll(t, s, []step{
		{coordinator, events.TaskWrappedUp, "", map[string]any{"summary": "The parser is half done, committed."}, true},
		{coordinator, events.TaskWrappedUp, "", map[string]any{"summary": "twice"}, false},
		{worker, events.TurnStarted, "turn-4", map[string]any{"agent": "tech-lead", "hash": "h"}, false}, // nothing more runs
	})
	task := taskIn(s)
	if !task.IsDone || !task.DoneByYou || task.WrapUp != nil || !task.Turns[1].Stopped || task.Summary != "The parser is half done, committed." {
		t.Errorf("done %v by you %v, wrap-up %+v, engineer stopped %v, summary %q", task.IsDone, task.DoneByYou, task.WrapUp, task.Turns[1].Stopped, task.Summary)
	}
}

// A task whose log can't be replayed takes no more events, not even a
// task.created that would start it again.
func TestATaskThatCantBeReplayedTakesNoMoreEvents(t *testing.T) {
	state := current.New(withIdeas, ideas.Streams())
	state.Loaded(0)
	var position int64
	apply := func(p events.Proposal) {
		position++
		state.Apply(events.Event{Position: position, Proposal: p})
	}
	for _, p := range agents.SeedProposals(nil) {
		apply(p)
	}
	apply(events.Proposal{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: created.data})
	apply(events.Proposal{Stream: "task-1", Actor: worker, Type: events.WorktreeMoved,
		Data: map[string]any{"worker": "laptop", "path": "/w", "port": 7901}})
	for _, st := range []step{created, {you, events.TaskRenamed, "", map[string]any{"title": "Again"}, false}} {
		rejection := Check(events.Proposal{Stream: "task-1", Actor: st.actor, Type: st.kind, Data: st.data}, state, withIdeas)
		if rejection == nil || !strings.Contains(rejection.Reason, "could not read the task: applying worktree.moved to task-1") {
			t.Errorf("%s: %v, want refused with where the replay stopped", st.kind, rejection)
		}
	}
}

// A turn your move to To do, the Backlog or Done stopped takes no more
// events: the agent's session may still be going, but its work is over.
// (Logs written before the move stopped the turn have them; apply replays
// those, see tasks' stopped_turn_test.go.)
func TestATurnYourMoveStoppedTakesNoMoreEvents(t *testing.T) {
	for name, move := range map[string]step{
		"to do":   {you, events.TaskHeld, "", map[string]any{"place": "todo"}, true},
		"backlog": {you, events.TaskHeld, "", map[string]any{"place": "backlog"}, true},
		"done":    {you, events.TaskDone, "", map[string]any{"summary": "Moved.", "moved": true}, true},
	} {
		t.Run(name, func(t *testing.T) {
			s := openSeeded(t)
			proposeAll(t, s, []step{
				created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "assign"),
				{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"}, true},
				turnStarted("turn-2", "engineer"),
				{engineer, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "ls"}, true},
				{policy, events.ActionApproved, "toolu_1", map[string]any{"reason": "otherwise"}, true},
				{engineer, events.ActionRequested, "toolu_2", map[string]any{"tool": "Bash", "command": "rm x"}, true},
				move,
				{worker, events.ActionResult, "toolu_1", nil, true},                             // approved before the move: it ran
				{policy, events.ActionAsked, "toolu_2", map[string]any{"reason": "ask"}, false}, // no one waits for it
				{policy, events.ActionDenied, "toolu_2", map[string]any{"reason": "couldn't ask you"}, true},
				{engineer, events.ActionRequested, "toolu_3", map[string]any{"tool": "Bash", "command": "pwd"}, false},
				{worker, events.PromptAcknowledged, "turn-2", map[string]any{"hash": "h-turn-2"}, false},
				{worker, events.SessionStarted, "turn-2", map[string]any{"engineSession": "s"}, false},
				{worker, events.PlanUpdated, "turn-2", map[string]any{"plan": "- [ ] more"}, false},
				{worker, events.AgentWaiting, "turn-2", map[string]any{"question": "more?"}, false},
				{worker, events.TurnEnded, "turn-2", map[string]any{"agent": "engineer", "report": "Done more."}, false},
				{worker, events.TurnStopped, "turn-2", nil, false},
			})
			if task := taskIn(s); task.OpenTurn() != nil || !task.Turns[1].Stopped || len(task.WaitingForYou()) != 0 || task.Action("toolu_3") != nil {
				t.Errorf("open turn %+v, engineer stopped %v, waiting %+v, toolu_3 %+v",
					task.OpenTurn(), task.Turns[1].Stopped, task.WaitingForYou(), task.Action("toolu_3"))
			}
		})
	}
}

func TestYourMoveToToDoStands(t *testing.T) {
	s := openSeeded(t)
	proposeAll(t, s, []step{
		created, worktree, turnStarted("turn-1", "tech-lead"),
		{you, events.TaskHeld, "", map[string]any{"place": "todo"}, true}, // the coordinator's turn stops
		turnStarted("turn-2", "tech-lead"),                                // its wrap-up
		turnEnded("turn-2", "tech-lead", `{"action":"assign"}`),
	})
	refusedWith(t, s, coordinator, events.TaskAssigned, map[string]any{"agent": "engineer", "instructions": "go"},
		"the user moved this task to To do; it waits there until they move it on")
	proposeAll(t, s, []step{{coordinator, events.TaskWrappedUp, "", map[string]any{"summary": "Nothing started yet."}, true}})
	if task := taskIn(s); task.Status() != tasks.Held || task.WrapUp != nil || !task.Turns[0].Stopped || task.Told.Text != "Nothing started yet." {
		t.Errorf("status %s, wrap-up %+v, first turn stopped %v, told %q", task.Status(), task.WrapUp, task.Turns[0].Stopped, task.Told.Text)
	}
}

// Your move to Review or back to In progress stands until the coordinator
// hands the task on, and it hands it to an agent that fits the move.
func TestYourMoveToReviewStands(t *testing.T) {
	s := openSeeded(t)
	assign := func(agent string, checks bool) map[string]any {
		return map[string]any{"agent": agent, "instructions": "go", "checks": checks}
	}
	proposeAll(t, s, []step{
		created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "assign"),
		{coordinator, events.TaskAssigned, "", assign("engineer", false), true},
		turnStarted("turn-2", "engineer"), turnEnded("turn-2", "engineer", "Built."),
		{you, events.TaskDirected, "", map[string]any{"text": "Have this task reviewed next.", "column": "review"}, true},
		turnStarted("turn-3", "tech-lead"), turnEnded("turn-3", "tech-lead", "assign"),
	})
	refusedWith(t, s, coordinator, events.TaskAssigned, assign("engineer", false),
		"the user moved this task to Review; hand it to an agent that checks work")
	proposeAll(t, s, []step{
		{coordinator, events.TaskAssigned, "", assign("reviewer", true), true},
		turnStarted("turn-4", "reviewer"), turnEnded("turn-4", "reviewer", "Looks fine."),
	})
	if task := taskIn(s); task.Column != "" {
		t.Errorf("the reviewer has it: column %q, want the stage's", task.Column)
	}

	proposeAll(t, s, []step{
		{you, events.TaskDirected, "", map[string]any{"text": "This needs more work.", "column": "in-progress", "sentBack": true}, true},
		turnStarted("turn-5", "tech-lead"), turnEnded("turn-5", "tech-lead", "assign"),
	})
	refusedWith(t, s, coordinator, events.TaskAssigned, assign("tester", true),
		"the user moved this task back to In progress; hand it to an agent that builds it")
	proposeAll(t, s, []step{{coordinator, events.TaskAssigned, "", assign("engineer", false), true}})

	// Started from To do rather than sent back from Review: anyone may take it.
	proposeAll(t, s, []step{
		turnStarted("turn-6", "engineer"), turnEnded("turn-6", "engineer", "Fixed."),
		{you, events.TaskHeld, "", map[string]any{"place": "todo"}, true},
		{you, events.TaskHeld, "", map[string]any{"place": "", "column": "in-progress"}, true},
		turnStarted("turn-7", "tech-lead"), turnEnded("turn-7", "tech-lead", "assign"),
		{coordinator, events.TaskAssigned, "", assign("reviewer", true), true},
	})
}

// A team with no agent that checks work can't have a task checked: its
// coordinator hands it to a builder, and the task stays in Review.
func TestReviewWithNoChecker(t *testing.T) {
	s := openSeeded(t)
	for _, name := range []string{"reviewer", "tester"} {
		if _, rejection, err := s.Propose(events.Proposal{Stream: events.AgentsStream, Actor: you, Type: events.AgentRemoved,
			Data: map[string]any{"name": name}}); rejection != nil || err != nil {
			t.Fatal(rejection, err)
		}
	}
	proposeAll(t, s, []step{
		created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "assign"),
		{you, events.TaskDirected, "", map[string]any{"text": "Have this task reviewed next.", "column": "review"}, true},
		{coordinator, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "check it", "checks": false}, true},
	})
	if task := taskIn(s); task.Column != "review" {
		t.Errorf("column %q, want it kept in review", task.Column)
	}
}

// Only your move gives a wrap-up, and only the board's Done finishes a
// story for you.
func TestNoWrapUpWithoutYourMove(t *testing.T) {
	s := openSeeded(t)
	proposeAll(t, s, []step{
		created, worktree, turnStarted("turn-1", "tech-lead"), turnEnded("turn-1", "tech-lead", "x"),
		{coordinator, events.TaskWrappedUp, "", map[string]any{"summary": "x"}, false},
	})
	refusedWith(t, s, you, events.TaskDone, map[string]any{"summary": "x"},
		"a task is finished by its coordinator; move it to Done on the board to have it finished as it is")
}

// proposeAll proposes the steps to task-1.
func proposeAll(t *testing.T, s *store.Store, steps []step) {
	t.Helper()
	for i, st := range steps {
		_, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: st.actor, Type: st.kind, Correlation: st.correlation, Data: st.data})
		if err != nil {
			t.Fatal(err)
		}
		if (rejection == nil) != st.accepted {
			t.Errorf("step %d %s by %s: accepted=%v (%v), want %v", i+1, st.kind, st.actor, rejection == nil, rejection, st.accepted)
		}
	}
}

func refusedWith(t *testing.T, s *store.Store, actor events.Actor, kind events.Type, data map[string]any, reason string) {
	t.Helper()
	_, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: actor, Type: kind, Data: data})
	if err != nil || rejection == nil || rejection.Reason != reason {
		t.Errorf("%s by %s: %v %v, want refused: %s", kind, actor, rejection, err, reason)
	}
}

func taskIn(s *store.Store) *tasks.Task {
	stream, _ := s.Stream("task-1")
	return tasks.FromEvents(stream)
}

// brokenStream is a state whose one stream could not be built.
type brokenStream struct {
	*current.State
	stream string
}

func (b brokenStream) Broken(stream string) error {
	if stream == b.stream {
		return errors.New("applying it panicked")
	}
	return b.State.Broken(stream)
}

// A roster whose stream broke refuses what would be checked against it,
// as an unreadable stream did when the rules replayed the log.
func TestABrokenRosterRefusesWhatItWouldCheck(t *testing.T) {
	_, joined := workers.Join("laptop", "a-code", workers.Machine{})
	_, connected := connections.Connect("a-code", connections.Machine{})
	cases := []struct {
		stream string
		p      events.Proposal
		want   string
	}{
		{events.AgentsStream, events.Proposal{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: created.data}, "could not read the agents: applying it panicked"},
		{events.WorkersStream, joined, "could not read the workers: applying it panicked"},
		{events.ConnectionsStream, connected, "could not read the connections: applying it panicked"},
	}
	for _, c := range cases {
		state := current.New(withIdeas, ideas.Streams())
		state.Loaded(0)
		if rejection := Check(c.p, brokenStream{state, c.stream}, withIdeas); rejection == nil || rejection.Reason != c.want {
			t.Errorf("%s broken, %s: %v, want %q", c.stream, c.p.Type, rejection, c.want)
		}
	}
}

// An archived task takes no events, whoever proposes them, and the
// rejection says why.
func TestAnArchivedTaskTakesNoEvents(t *testing.T) {
	state := current.New(withIdeas, ideas.Streams())
	s, err := store.Open(filepath.Join(t.TempDir(), "agentry.db"), withIdeas, state, func(p events.Proposal) *events.Rejection { return Check(p, state, withIdeas) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, p := range append(agents.SeedProposals(nil), events.Proposal{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: created.data}) {
		if _, rejection, err := s.Propose(p); rejection != nil || err != nil {
			t.Fatal(p.Type, rejection, err)
		}
	}
	if _, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: you, Type: events.TaskDone, Data: map[string]any{"summary": "s", "moved": true}}); rejection != nil || err != nil {
		t.Fatal(rejection, err)
	}
	archive(t, s, state, "task-1")

	for _, p := range []events.Proposal{
		{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: created.data},
		{Stream: "task-1", Actor: you, Type: events.TaskRanked, Data: map[string]any{"rank": 1}},
		{Stream: "task-1", Actor: events.Actor{Kind: events.Agent, ID: "tech-lead"}, Type: events.TaskDone, Data: map[string]any{"summary": "s"}},
		{Stream: "task-1", Actor: events.Actor{Kind: events.Worker, ID: "local"}, Type: events.WorktreeReady, Data: map[string]any{"path": "/w", "branch": "b", "port": 7901}},
	} {
		if _, rejection, err := s.Propose(p); err != nil || rejection == nil || rejection.Reason != "task-1 is archived: an archived task takes no events" {
			t.Errorf("%s on an archived task: %v, %v", p.Type, rejection, err)
		}
	}
}

// A new task goes only into the stream Store.Create names: the next task
// stream, never one further on or one a task has already. A refused one
// leaves nothing in its stream.
func TestATaskIsCreatedOnlyInTheNextTaskStream(t *testing.T) {
	s := openSeeded(t)
	create := func(stream string) *events.Rejection {
		t.Helper()
		_, rejection, err := s.Propose(events.Proposal{Stream: stream, Actor: you, Type: events.TaskCreated, Data: created.data})
		if err != nil {
			t.Fatal(err)
		}
		return rejection
	}
	if rejection := create("task-1"); rejection != nil {
		t.Fatalf("task-1, the next: %v", rejection)
	}
	for stream, want := range map[string]string{
		"task-6": "a new task goes into task-2, the next task stream, not task-6",
		"task-1": "task task-1 already exists",
	} {
		if rejection := create(stream); rejection == nil || rejection.Reason != want {
			t.Errorf("%s: %v, want %q", stream, rejection, want)
		}
	}
	if stream, _ := s.Stream("task-6"); len(stream) != 0 {
		t.Errorf("a refused task.created is stored in task-6: %v", stream)
	}
	if stream, _ := s.Stream("task-1"); len(stream) != 1 {
		t.Errorf("task-1 has %d events, want its one task.created", len(stream))
	}
	made, rejection, err := s.Create(events.TaskPrefix, events.Proposal{Actor: you, Type: events.TaskCreated, Data: created.data})
	if err != nil || rejection != nil || made.Stream != "task-2" {
		t.Errorf("Store.Create after the refusals: %s %v %v, want task-2", made.Stream, rejection, err)
	}
}

// On a build without ideas, idea-3 is a foreign stream: the rules refuse
// anything in it, a task.created too, even where the shape check would not.
func TestNothingGoesIntoAForeignStream(t *testing.T) {
	state := current.New(events.Core())
	state.Loaded(0)
	created := events.Proposal{Stream: "idea-3", Actor: you, Type: events.TaskCreated,
		Data: map[string]any{"title": "x", "repo": "/r", "coordinator": "tech-lead", "budget": 0, "maxTurns": 0}}
	renamed := events.Proposal{Stream: "idea-3", Actor: you, Type: events.TaskRenamed, Data: map[string]any{"title": "y"}}
	dismissed := events.Proposal{Stream: "idea-3", Actor: you, Type: ideas.IdeaDismissed, Data: map[string]any{"note": "no"}}
	for _, p := range []events.Proposal{created, renamed, dismissed} {
		rejection := Check(p, state, events.Core())
		if rejection == nil || rejection.Reason != "stream idea-3 belongs to a feature this build doesn't have" {
			t.Errorf("%s into idea-3: %v, want the foreign stream's refusal", p.Type, rejection)
		}
	}
	if rejection := Check(dismissed, state, withIdeas); rejection != nil && rejection.Reason == events.ForeignReason("idea-3") {
		t.Errorf("with ideas registered, idea-3 is refused as foreign: %v", rejection)
	}
}
