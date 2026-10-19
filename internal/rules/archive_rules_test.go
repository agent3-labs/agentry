package rules

import (
	"errors"
	"testing"

	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/tasks/taskstest"
)

var archiver = events.Actor{Kind: events.System, ID: "archiver"}

const blob = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func archivedData(change func(map[string]any)) map[string]any {
	data := map[string]any{"through": 9, "count": 3, "archive": blob, "created": 6, "summary": map[string]any{"title": "Remove the TODOs"}}
	if change != nil {
		change(data)
	}
	return data
}

func unarchivedData(change func(map[string]any)) map[string]any {
	data := map[string]any{"marker": 10, "archive": blob}
	if change != nil {
		change(data)
	}
	return data
}

// openArchived is a store with task-1 made, done and archived by its
// marker, and the state that holds it as its summary.
func openArchived(t *testing.T) (*store.Store, *current.State) {
	t.Helper()
	s, state := openSeededState(t)
	for _, p := range []events.Proposal{
		{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: created.data},
		{Stream: "task-1", Actor: coordinator, Type: events.TaskDone, Data: map[string]any{"summary": "Gone."}},
	} {
		if _, rejection, err := s.Propose(p); rejection != nil || err != nil {
			t.Fatal(p.Type, rejection, err)
		}
	}
	archive(t, s, state, "task-1")
	return s, state
}

// archive archives a done task with a marker the rules accept.
func archive(t *testing.T, s *store.Store, state *current.State, id string) {
	t.Helper()
	task, err := state.Task(id)
	if err != nil {
		t.Fatal(err)
	}
	own, err := s.Stream(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, rejection, err := s.Propose(taskstest.Marker(own, tasks.SummaryOf(task, state.Roster()))); rejection != nil || err != nil {
		t.Fatal(rejection, err)
	}
}

// An archived task takes its unarchiving and nothing else; once unarchived
// it is whole again and takes events as before.
func TestAnArchivedTaskTakesOnlyItsUnarchiving(t *testing.T) {
	s, state := openArchived(t)
	for _, p := range []events.Proposal{
		{Stream: "task-1", Actor: archiver, Type: events.TaskArchived, Data: archivedData(nil)},
		{Stream: "task-1", Actor: you, Type: events.TaskReopened, Data: map[string]any{"text": "More."}},
		{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: created.data},
	} {
		if _, rejection, err := s.Propose(p); err != nil || rejection == nil || rejection.Reason != "task-1 is archived: an archived task takes no events" {
			t.Errorf("%s on an archived task: %v, %v", p.Type, rejection, err)
		}
	}
	for _, c := range []struct {
		data map[string]any
		want string
	}{
		{unarchivedData(func(d map[string]any) { d["archive"] = "abc" }), "not an archive's hash"},
		{unarchivedData(func(d map[string]any) { d["marker"] = 0 }), "an unarchiving names the position of the marker it undoes"},
	} {
		p := events.Proposal{Stream: "task-1", Actor: archiver, Type: events.TaskUnarchived, Data: c.data}
		if _, rejection, err := s.Propose(p); err != nil || rejection == nil || rejection.Reason != c.want {
			t.Errorf("unarchiving with %v: %v, %v, want %q", c.data, rejection, err, c.want)
		}
	}

	if _, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: archiver, Type: events.TaskUnarchived, Data: unarchivedData(nil)}); rejection != nil || err != nil {
		t.Fatalf("unarchiving: %v, %v", rejection, err)
	}
	if task, err := state.Task("task-1"); task == nil || err != nil || !task.IsDone {
		t.Fatalf("after unarchiving, task-1 should be whole and done again: %v, %v", task, err)
	}
	if _, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: you, Type: events.TaskReopened, Data: map[string]any{"text": "More."}}); rejection != nil || err != nil {
		t.Errorf("reopening the unarchived task: %v, %v", rejection, err)
	}
}

// Only a done task is archived, with a marker that says where its rows
// went; a whole task has nothing to unarchive.
func TestOnlyADoneTaskIsArchived(t *testing.T) {
	s, state := openSeededState(t)
	if _, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: you, Type: events.TaskCreated, Data: created.data}); rejection != nil || err != nil {
		t.Fatal(rejection, err)
	}
	propose := func(kind events.Type, data map[string]any) *events.Rejection {
		t.Helper()
		_, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: archiver, Type: kind, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		return rejection
	}
	if rejection := propose(events.TaskArchived, archivedData(nil)); rejection == nil || rejection.Reason != "the task isn't done, so it can't be archived" {
		t.Errorf("archiving an open task: %v", rejection)
	}
	if _, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: coordinator, Type: events.TaskDone, Data: map[string]any{"summary": "Gone."}}); rejection != nil || err != nil {
		t.Fatal(rejection, err)
	}
	for _, c := range []struct {
		data map[string]any
		want string
	}{
		{archivedData(func(d map[string]any) { d["archive"] = "" }), "not an archive's hash"},
		{archivedData(func(d map[string]any) { d["created"] = 0 }), `an archive covers the task's rows from its task.created to its "through"`},
		{archivedData(func(d map[string]any) { d["through"] = 5 }), `an archive covers the task's rows from its task.created to its "through"`},
		{archivedData(func(d map[string]any) { d["count"] = 0 }), "an archive holds the task's rows, its task.created at least"},
	} {
		if rejection := propose(events.TaskArchived, c.data); rejection == nil || rejection.Reason != c.want {
			t.Errorf("archiving with %v: %v, want %q", c.data, rejection, c.want)
		}
	}
	if rejection := propose(events.TaskUnarchived, unarchivedData(nil)); rejection == nil || rejection.Reason != "task task-1 isn't archived" {
		t.Errorf("unarchiving a whole task: %v", rejection)
	}
	if rejection := propose(events.TaskArchived, archivedData(nil)); rejection != nil {
		t.Errorf("archiving the done task: %v", rejection)
	}
	if task, err := state.Task("task-1"); task != nil || !errors.Is(err, current.ErrArchived) {
		t.Errorf("an accepted marker archives the task: %v, %v", task, err)
	}
}

// Only the store writes the markers: no agent, no worker, not the page.
func TestOnlyTheStoreWritesTheMarkers(t *testing.T) {
	s, _ := openArchived(t)
	for _, actor := range []events.Actor{you, coordinator, engineer, worker, policy} {
		for kind, data := range map[events.Type]map[string]any{events.TaskArchived: archivedData(nil), events.TaskUnarchived: unarchivedData(nil)} {
			_, rejection, err := s.Propose(events.Proposal{Stream: "task-1", Actor: actor, Type: kind, Data: data})
			if err != nil || rejection == nil || rejection.Check != "who" {
				t.Errorf("%s by %s: %v, %v, want refused by the who check", kind, actor, rejection, err)
			}
		}
	}
}
