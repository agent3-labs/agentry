package current

import (
	"errors"
	"slices"
	"testing"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/ideas"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/tasks/taskstest"
)

// A refold rebuilds its stream from the events before it and itself, as a
// replay does: for each stream, the state is loaded without the stream's
// last event, and that event comes last, as a refold.
func TestARefoldEqualsAReplay(t *testing.T) {
	for _, stream := range streamsOf(everyStream()) {
		log := everyStream()
		own := accepted(log, stream)
		if len(own) == 0 {
			continue // task-9 has only a refusal
		}
		last := own[len(own)-1]
		log = slices.DeleteFunc(log, func(e events.Event) bool { return e.Position == last.Position })
		state := loaded(log)
		last.Position = everyStream()[len(everyStream())-1].Position + 1
		state.Refold(stream, own[:len(own)-1], last)
		t.Run(stream, func(t *testing.T) { sameAsReplay(t, state, append(log, last)) })
	}
}

// A task.unarchived takes back the task its marker set aside, rebuilt from
// the restored rows before the marker, and the summary goes; with no rows
// before the marker there is no task to take back, and the stream breaks.
func TestUnarchivingTakesBackTheWholeTask(t *testing.T) {
	log := withArchived(false)
	state := loaded(log)
	unarchived := events.Event{Position: 101, At: time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC), Proposal: taskstest.Unarchived("task-1", 100)}
	state.Refold("task-1", accepted(log, "task-1"), unarchived)
	sameAsReplay(t, state, append(log, unarchived))
	if task, err := state.Task("task-1"); task == nil || err != nil {
		t.Errorf("task-1 should be whole again: %v, %v", task, err)
	}
	if summaries := state.Summaries(); len(summaries) != 2 || summaries[0].ID != "task-1" || summaries[0].LastActiveAt != unarchived.At {
		t.Errorf("the summaries are %+v, want task-1's made from the task again", summaries)
	}

	rowsGone := withArchived(true)
	state = loaded(rowsGone)
	state.Refold("task-1", accepted(rowsGone, "task-1"), unarchived)
	if _, err := state.Task("task-1"); err == nil || errors.Is(err, ErrArchived) {
		t.Errorf("unarchiving with no rows before the marker: %v, want the stream broken", err)
	}
}

// A refold of a broken stream folds the event that broke it again, so the
// stream stays broken, as its replay does.
func TestARefoldOfABrokenStreamStaysBroken(t *testing.T) {
	log := logOf(
		proposal("task-1", you, events.TaskCreated, map[string]any{"title": "One", "repo": "/r", "coordinator": "tech-lead"}),
		proposal("task-1", worker, events.WorktreeMoved, map[string]any{"worker": "laptop", "path": "/w", "port": float64(7901)}),
		proposal("task-1", you, events.TaskRenamed, map[string]any{"title": "Renamed"}),
	)
	state := loaded(log[:2])
	if state.Broken("task-1") == nil {
		t.Fatal("task-1 should be broken")
	}
	state.Refold("task-1", log[:2], log[2])
	if _, err := state.Task("task-1"); err == nil {
		t.Error("the refold mended a stream its replay can't build")
	}
	if state.Position() != 3 || state.StreamPosition("task-1") != 3 {
		t.Errorf("the refold left the position at %d, task-1's at %d", state.Position(), state.StreamPosition("task-1"))
	}
}

// After a refold the state still equals a replay of the log: an archived
// task is back in full, and a broken one is still broken.
func TestEqualHoldsAfterARefold(t *testing.T) {
	s := openStore(t, someProposals()...)
	state := New(withIdeas, ideas.Streams())
	state.Loaded(0)
	fromTheLog(t, s, state)
	own, err := s.Stream("task-1")
	if err != nil {
		t.Fatal(err)
	}
	marker, _, err := s.Propose(taskstest.Marker(own, tasks.SummaryOf(tasks.FromEvents(own), nil)))
	if err != nil {
		t.Fatal(err)
	}
	fromTheLog(t, s, state)
	if err := Equal(s, state); err != nil {
		t.Errorf("archived: %v", err)
	}
	for _, p := range []events.Proposal{taskstest.Unarchived("task-1", marker.Position), proposal("task-3", you, events.TaskLabeled, map[string]any{"labels": []any{"refolded"}})} {
		before, err := s.Stream(p.Stream)
		if err != nil {
			t.Fatal(err)
		}
		e, _, err := s.Propose(p)
		if err != nil {
			t.Fatal(err)
		}
		state.Refold(p.Stream, before, e)
	}
	if err := Equal(s, state); err != nil {
		t.Error(err)
	}
	if task, err := state.Task("task-1"); err != nil || task == nil || !slices.Equal(task.Labels, []string{"web"}) {
		t.Errorf("task-1 after its refold: %+v, %v", task, err)
	}
	if state.Broken("task-3") == nil {
		t.Error("task-3 should still be broken")
	}
}
