package storetest

import (
	"testing"

	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/tasks/taskstest"
)

// Archive archives a task as the archiver will (docs/design/log-growth.md
// section 4), with a marker the rules check and the state folds: the
// task's summary as the list showed it. Only a done task is archived, so
// a task that isn't done is moved to Done first. The rows the marker
// covers stay in the log, which the state folds the same way: deleting
// them is the archiver's (build step 7).
func Archive(t testing.TB, s *store.Store, state *current.State, id string) {
	t.Helper()
	task, err := state.Task(id)
	if err != nil || task == nil {
		t.Fatalf("archiving %s: %v, %v", id, task, err)
	}
	if !task.IsDone {
		mustPropose(t, s, events.Proposal{Stream: id, Actor: events.Actor{Kind: events.Human, ID: "you"}, Type: events.TaskDone,
			Data: map[string]any{"summary": "Done, to archive.", "moved": true}})
		task, _ = state.Task(id)
	}
	accepted, err := s.Stream(id)
	if err != nil {
		t.Fatal(err)
	}
	mustPropose(t, s, taskstest.Marker(accepted, tasks.SummaryOf(task, state.Roster())))
}

func mustPropose(t testing.TB, s *store.Store, p events.Proposal) {
	t.Helper()
	if _, rejection, err := s.Propose(p); rejection != nil || err != nil {
		t.Fatalf("%s on %s: %v, %v", p.Type, p.Stream, rejection, err)
	}
}
