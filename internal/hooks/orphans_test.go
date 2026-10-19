package hooks

import (
	"path/filepath"
	"testing"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/storetest"
)

// A broken task before the one with an orphaned ask doesn't stop the start:
// it is skipped, and the ask after it is still held.
func TestARestartSkipsABrokenTask(t *testing.T) {
	state := current.New(events.Core())
	// No rules: they would refuse the worktree.moved that breaks task-1.
	s, err := store.Open(filepath.Join(t.TempDir(), "agentry.db"), events.Core(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	you := events.Actor{Kind: events.Human, ID: "you"}
	engineer := events.Actor{Kind: events.Agent, ID: "engineer"}
	created := map[string]any{"title": "t", "repo": "/r", "coordinator": "tech-lead"}
	for _, p := range []events.Proposal{
		{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: created},
		{Stream: "task-1", Actor: worker, Type: events.WorktreeMoved, Data: map[string]any{"worker": "local", "path": "/w", "port": 7901}},
		{Stream: "task-2", Actor: you, Type: events.TaskCreated, Data: created},
		{Stream: "task-2", Actor: worker, Type: events.WorktreeReady, Data: map[string]any{"path": "/w/task-2", "branch": "b", "port": 7902}},
		{Stream: "task-2", Actor: worker, Type: events.TurnStarted, Correlation: "turn-1", Data: map[string]any{"agent": "engineer", "hash": "h"}},
		{Stream: "task-2", Actor: engineer, Type: events.ActionRequested, Correlation: "toolu_1", Data: map[string]any{"tool": "Bash", "command": "curl x"}},
		{Stream: "task-2", Actor: ruleset, Type: events.ActionAsked, Correlation: "toolu_1", Data: map[string]any{"reason": "may reach outside"}},
	} {
		if _, rejection, err := s.Propose(p); rejection != nil || err != nil {
			t.Fatal(p.Type, rejection, err)
		}
	}
	if state.Broken("task-1") == nil {
		t.Fatal("task-1 should be broken")
	}

	if err := HoldOrphanedAsks(s, state); err != nil {
		t.Fatal(err)
	}
	task, _ := state.Task("task-2")
	if action := task.Action("toolu_1"); action == nil || !action.Held {
		t.Errorf("task-2's ask should be held, got %+v", action)
	}
}

// An archived task is done, so a restart skips it as it skips a done task,
// without reading its stream: its ask is left as it is, and nothing is
// proposed, so the rules have nothing to refuse.
func TestARestartSkipsAnArchivedTask(t *testing.T) {
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	you := events.Actor{Kind: events.Human, ID: "you"}
	engineer := events.Actor{Kind: events.Agent, ID: "engineer"}
	for _, p := range []events.Proposal{
		{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: map[string]any{"title": "t", "repo": "/r", "coordinator": "tech-lead"}},
		{Stream: "task-1", Actor: worker, Type: events.WorktreeReady, Data: map[string]any{"path": "/w/task-1", "branch": "b", "port": 7901}},
		{Stream: "task-1", Actor: worker, Type: events.TurnStarted, Correlation: "turn-1", Data: map[string]any{"agent": "engineer", "hash": "h"}},
		{Stream: "task-1", Actor: engineer, Type: events.ActionRequested, Correlation: "toolu_1", Data: map[string]any{"tool": "Bash", "command": "curl x"}},
		{Stream: "task-1", Actor: ruleset, Type: events.ActionAsked, Correlation: "toolu_1", Data: map[string]any{"reason": "may reach outside"}},
	} {
		if _, rejection, err := s.Propose(p); rejection != nil || err != nil {
			t.Fatal(p.Type, rejection, err)
		}
	}
	storetest.Archive(t, s, state, "task-1")
	before := s.LastPosition()

	if err := HoldOrphanedAsks(s, state); err != nil {
		t.Fatal(err)
	}
	if after := s.LastPosition(); after != before {
		t.Errorf("the restart wrote %d events for an archived task", after-before)
	}
}
