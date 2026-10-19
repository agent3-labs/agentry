package current

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/connections"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/ideas"
	"gitlab.com/dev.chempa/agentry/internal/settings"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/tasks/taskstest"
	"gitlab.com/dev.chempa/agentry/internal/workers"
)

var (
	you    = events.Actor{Kind: events.Human, ID: "you"}
	lead   = events.Actor{Kind: events.Agent, ID: "tech-lead"}
	worker = events.Actor{Kind: events.Worker, ID: "local"}
	system = events.Actor{Kind: events.System, ID: "loop"}
)

// logOf numbers proposals into a log, one second apart.
func logOf(proposals ...events.Proposal) []events.Event {
	log := make([]events.Event, len(proposals))
	for i, p := range proposals {
		log[i] = events.Event{Position: int64(i + 1), At: time.Date(2026, 10, 18, 9, 0, i, 0, time.UTC), Proposal: p}
	}
	return log
}

func proposal(stream string, actor events.Actor, kind events.Type, data map[string]any) events.Proposal {
	return events.Proposal{Stream: stream, Actor: actor, Type: kind, Data: data}
}

func rejected(stream string, kind events.Type) events.Proposal {
	return proposal(stream, events.Actor{Kind: events.System, ID: "validator"}, events.ProposalRejected,
		map[string]any{"type": string(kind), "by": "human:you", "check": "order", "reason": "no"})
}

// everyStream is a log touching every kind of stream, interleaved, with
// rejections in a task's stream and in one never created.
func everyStream() []events.Event {
	return logOf(
		proposal(events.AgentsStream, system, events.AgentCreated, map[string]any{"name": "tech-lead", "prompt": "Lead.", "engine": "claude", "coordinates": true}),
		proposal("task-1", you, events.TaskCreated, map[string]any{"title": "Speed up the list", "repo": "/r", "coordinator": "tech-lead"}),
		proposal(events.WorkersStream, you, events.WorkerInvited, map[string]any{"code": "c1", "expires": "2026-10-18T10:00:00Z"}),
		proposal(events.AgentsStream, you, events.AgentCreated, map[string]any{"name": "engineer", "prompt": "Build.", "engine": "claude"}),
		rejected("task-1", events.TaskRenamed),
		proposal(events.PolicyStream, system, events.PolicyLoaded, map[string]any{"hash": "h", "path": "/p"}),
		proposal("task-1", you, events.TaskLabeled, map[string]any{"labels": []any{"web"}}),
		proposal(events.WorkersStream, system, events.WorkerJoined, map[string]any{"name": "laptop", "code": "c1", "credential": "k"}),
		proposal("task-2", you, events.TaskCreated, map[string]any{"title": "Second", "repo": "/r", "coordinator": "tech-lead"}),
		rejected("task-9", events.TaskCreated),
		proposal(events.ConnectionsStream, you, events.ConnectionInvited, map[string]any{"code": "c2", "expires": "2026-10-18T10:00:00Z"}),
		proposal("task-1", lead, events.TaskAssigned, map[string]any{"agent": "engineer", "instructions": "Profile it."}),
		proposal(events.AgentsStream, you, events.AgentLearned, map[string]any{"name": "engineer", "knowledge": "Measure first."}),
		agents.Agent{Name: "engineer"}.Patching(you, agents.Diff("Measure first.", "Measure first, then cut.")),
		proposal(events.ConnectionsStream, system, events.ConnectionMade, map[string]any{"id": "m1", "code": "c2", "credential": "k2"}),
		proposal(events.WorkersStream, system, events.WorkerConnected, map[string]any{"name": "laptop", "engines": []any{"claude 2"}}),
		proposal("task-2", lead, events.TaskRenamed, map[string]any{"title": "Second, renamed"}),
		proposal(events.AgentsStream, you, events.AgentRemoved, map[string]any{"name": "tech-lead"}),
		proposal("task-1", worker, events.TurnStarted, map[string]any{"agent": "engineer", "hash": "h"}),
		proposal("idea-1", system, ideas.IdeaSuggested, map[string]any{"title": "Faster", "problem": "Slow", "proposal": "Cache", "size": "S", "repo": "/r", "task": "task-1", "backedBy": []any{"engineer"}}),
		proposal("idea-1", you, ideas.IdeaAccepted, map[string]any{"task": "task-2", "note": "Yes"}),
		proposal(events.SettingsStream, you, events.SettingsChanged, map[string]any{settings.VisionariesOnEpicClose: false}),
		proposal(events.SettingsStream, you, events.SettingsChanged, map[string]any{settings.AutoAcceptRule: settings.NobodyDoubted}),
	)
}

// accepted is a stream's events as Store.Stream gives them.
func accepted(log []events.Event, stream string) []events.Event {
	var found []events.Event
	for _, e := range log {
		if e.Stream == stream && e.Type != events.ProposalRejected {
			found = append(found, e)
		}
	}
	return found
}

// streamsOf names every stream in the log, in the order first seen.
func streamsOf(log []events.Event) []string {
	var streams []string
	for _, e := range log {
		if !slices.Contains(streams, e.Stream) {
			streams = append(streams, e.Stream)
		}
	}
	return streams
}

// loaded is a state loaded from the log, as the store does at Open.
func loaded(log []events.Event) *State { return loadedWith(withIdeas, log) }

// loadedWith is loaded, reading the streams as catalogue says.
func loadedWith(catalogue *events.Catalogue, log []events.Event) *State {
	return loadInto(New(catalogue, ideas.Streams()), log)
}

// loadInto loads the log into s, as the store does at Open.
func loadInto(s *State, log []events.Event) *State {
	for _, stream := range streamsOf(log) {
		s.Load(stream, accepted(log, stream))
	}
	var last int64
	if len(log) > 0 {
		last = log[len(log)-1].Position
	}
	s.Loaded(last)
	return s
}

// sameAsReplay fails the test where the state differs from a replay of the
// log, or either one is broken.
func sameAsReplay(t *testing.T, s *State, log []events.Event) {
	t.Helper()
	for _, stream := range streamsOf(log) {
		if ideas.IsStream(stream) {
			idea, err := ideas.Get(s, stream)
			if want := ideas.FromEvents(accepted(log, stream)); err != nil || !reflect.DeepEqual(idea, want) {
				t.Errorf("%s: %+v, %v\nreplay: %+v", stream, idea, err, want)
			}
			continue
		}
		if !withIdeas.IsTaskStream(stream) {
			continue
		}
		task, err := s.Task(stream)
		if want, archived := tasks.Archived(accepted(log, stream)); archived {
			if summary, held := s.archived[stream]; !held || !errors.Is(err, ErrArchived) || !reflect.DeepEqual(summary, want) {
				t.Errorf("%s: %+v, %v\nreplay: archived as %+v", stream, summary, err, want)
			}
			continue
		}
		if want, replayErr := tasks.Replay(accepted(log, stream)); err != nil || replayErr != nil || !reflect.DeepEqual(task, want) {
			t.Errorf("%s: %+v, %v\nreplay: %+v, %v", stream, task, err, want, replayErr)
		}
	}
	if want := agents.FromEvents(accepted(log, events.AgentsStream)); !reflect.DeepEqual(s.Roster(), want) {
		t.Errorf("agents: %+v\nreplay: %+v", s.Roster(), want)
	}
	if want := workers.FromEvents(accepted(log, events.WorkersStream)); !reflect.DeepEqual(s.Workers(), want) {
		t.Errorf("workers: %+v\nreplay: %+v", s.Workers(), want)
	}
	if want := connections.FromEvents(accepted(log, events.ConnectionsStream)); !reflect.DeepEqual(s.Connections(), want) {
		t.Errorf("connections: %+v\nreplay: %+v", s.Connections(), want)
	}
	if want := settings.FromEvents(accepted(log, events.SettingsStream)); s.Settings() != want || want.VisionariesOnEpicClose ||
		want.AutoAcceptRule != settings.NobodyDoubted {
		t.Errorf("settings: %+v\nreplay: %+v", s.Settings(), want)
	}
	for _, stream := range streamsOf(log) {
		var want int64
		if own := accepted(log, stream); len(own) > 0 {
			want = own[len(own)-1].Position
		}
		if position := s.StreamPosition(stream); position != want {
			t.Errorf("%s: last position %d, want %d", stream, position, want)
		}
	}
	if want := log[len(log)-1].Position; s.Position() != want {
		t.Errorf("position %d, want %d", s.Position(), want)
	}
}

// Whatever part of the log is loaded at the start and whatever part is
// applied after, the state is what a replay of the whole log gives.
func TestLoadThenApplyEqualsAReplay(t *testing.T) {
	log := everyStream()
	for split := range len(log) + 1 {
		s := loaded(log[:split])
		for _, e := range log[split:] {
			s.Apply(e)
		}
		sameAsReplay(t, s, log)
		if t.Failed() {
			t.Fatalf("loading the first %d events", split)
		}
	}
}

func TestARejectionMovesOnlyThePosition(t *testing.T) {
	log := everyStream()
	s := loaded(log)
	before, _ := s.Task("task-1")
	for i, p := range []events.Proposal{rejected("task-1", events.TaskRenamed), rejected("task-7", events.TaskCreated), rejected(events.AgentsStream, events.AgentRemoved)} {
		e := events.Event{Position: int64(50 + i), Proposal: p}
		s.Apply(e)
		log = append(log, e)
	}
	if after, _ := s.Task("task-1"); after != before {
		t.Errorf("a rejection changed task-1: %+v", after)
	}
	if task, err := s.Task("task-7"); task != nil || err != nil || len(s.Tasks()) != 2 {
		t.Errorf("a rejected task.created made a task: %+v, %v, %d tasks", task, err, len(s.Tasks()))
	}
	if s.Position() != 52 {
		t.Errorf("position %d, want 52", s.Position())
	}
	sameAsReplay(t, s, log)
}

// A worktree.moved before any worktree.ready panics in tasks' apply (the
// rules never let it through). Only that task's reads fail.
func TestAPanicBreaksOnlyItsStream(t *testing.T) {
	log := everyStream()
	broken := logOf(
		proposal("task-3", you, events.TaskCreated, map[string]any{"title": "Third", "repo": "/r", "coordinator": "tech-lead"}),
		proposal("task-3", worker, events.WorktreeMoved, map[string]any{"worker": "laptop", "path": "/w", "port": float64(7901)}),
	)
	for _, e := range broken {
		e.Position += 100
		log = append(log, e)
	}
	more := proposal("task-1", lead, events.TaskRenamed, map[string]any{"title": "Still applied"})
	after := proposal("task-3", lead, events.TaskRenamed, map[string]any{"title": "Never applied"})

	breaks := map[string]error{}
	for name, s := range map[string]*State{"applied": applied(log), "loaded": loaded(log)} {
		s.Apply(events.Event{Position: 200, Proposal: more})
		s.Apply(events.Event{Position: 201, Proposal: after})
		task, err := s.Task("task-3")
		if task != nil || err == nil || !strings.Contains(err.Error(), "task-3") {
			t.Errorf("%s: task-3 should fail with its stream's error, got %+v, %v", name, task, err)
		}
		breaks[name] = err
		if task, err := s.Task("task-1"); err != nil || task.Title != "Still applied" {
			t.Errorf("%s: task-1 should carry on, got %+v, %v", name, task, err)
		}
		if ids := idsOf(s.Tasks()); !slices.Equal(ids, []string{"task-1", "task-2"}) {
			t.Errorf("%s: Tasks should leave the broken task out, got %v", name, ids)
		}
		if order := s.TaskIDs(); !slices.Equal(order, []string{"task-1", "task-2", "task-3"}) {
			t.Errorf("%s: the task order should still list task-3, as the log does: %v", name, order)
		}
		if s.Position() != 201 {
			t.Errorf("%s: position %d", name, s.Position())
		}
	}
	// Broken at Load, by tasks.Replay, or later by Apply's guard: the same answer.
	if a, l := fmt.Sprint(breaks["applied"]), fmt.Sprint(breaks["loaded"]); a != l {
		t.Errorf("task-3 broke differently when applied (%s) and loaded (%s)", a, l)
	}
}

// The log lists every task.created, whatever its stream and even on a
// broken one, so loading and applying count them all too.
func TestTheTaskOrderCountsEveryTaskCreated(t *testing.T) {
	log := everyStream()
	for i, p := range []events.Proposal{
		proposal("task-3", you, events.TaskCreated, map[string]any{"title": "Third", "repo": "/r", "coordinator": "tech-lead"}),
		proposal("task-3", worker, events.WorktreeMoved, map[string]any{"worker": "laptop", "path": "/w", "port": float64(7901)}),
		proposal(events.PolicyStream, you, events.TaskCreated, map[string]any{"title": "Odd", "repo": "/r", "coordinator": "tech-lead"}),
		proposal("task-3", you, events.TaskCreated, map[string]any{"title": "Again", "repo": "/r", "coordinator": "tech-lead"}),
	} {
		log = append(log, events.Event{Position: int64(100 + i), Proposal: p})
	}
	want := []string{"task-1", "task-2", "task-3", events.PolicyStream, "task-3"}
	for name, s := range map[string]*State{"applied": applied(log), "loaded": loaded(log)} {
		if order := s.TaskIDs(); !slices.Equal(order, want) {
			t.Errorf("%s: task order %v, want %v", name, order, want)
		}
	}
}

// withArchived is everyStream with task-1 archived at the end: its marker
// keeps the summary the list showed. With rowsGone, task-1's rows are gone
// from the log, as the archiver leaves them, and the marker alone stands
// for the task.
func withArchived(rowsGone bool) []events.Event {
	log := everyStream()
	own := accepted(log, "task-1")
	marker := taskstest.Marker(own, tasks.SummaryOf(tasks.FromEvents(own), nil))
	if rowsGone {
		log = slices.DeleteFunc(log, func(e events.Event) bool { return e.Stream == "task-1" })
	}
	return append(log, events.Event{Position: 100, At: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC), Proposal: marker})
}

// An archived task says so, leaves the tasks and still counts in the task
// order and for the next task, with its rows in the log or not, loaded or
// applied; its summary is in the list in its place. A broken task keeps
// its own error.
func TestAnArchivedTaskIsCountedButNotHandedOut(t *testing.T) {
	// applied from the start, loaded, and applied after a cut where the
	// marker is the first of its stream the state sees
	tail := func(log []events.Event) *State {
		cut := slices.IndexFunc(log, func(e events.Event) bool { return e.Stream == "task-2" })
		return applyInto(loaded(log[:cut]), log[cut:])
	}
	for _, rowsGone := range []bool{false, true} {
		log := withArchived(rowsGone)
		for name, s := range map[string]*State{"applied": applied(log), "loaded": loaded(log), "applied after a cut": tail(log)} {
			name = fmt.Sprintf("%s, rows gone %v", name, rowsGone)
			sameAsReplay(t, s, log)
			if task, err := s.Task("task-1"); task != nil || !errors.Is(err, ErrArchived) {
				t.Errorf("%s: archived task-1: %v, %v", name, task, err)
			}
			if ids := idsOf(s.Tasks()); !slices.Equal(ids, []string{"task-2"}) {
				t.Errorf("%s: the tasks are %v, want only task-2", name, ids)
			}
			if order := s.TaskIDs(); !slices.Equal(order, []string{"task-1", "task-2"}) {
				t.Errorf("%s: the task order is %v, want both tasks", name, order)
			}
			if next := s.NextTask(); next != "task-3" {
				t.Errorf("%s: the next task is %s, want task-3", name, next)
			}
			summaries := s.Summaries()
			if len(summaries) != 2 || summaries[0].ID != "task-1" || summaries[0].Title != "Speed up the list" || summaries[1].ID != "task-2" {
				t.Errorf("%s: the summaries are %+v, want task-1's as archived, then task-2's", name, summaries)
			}
		}
	}
	s := loaded(withArchived(true))
	s.Apply(events.Event{Position: 101, Proposal: proposal("task-2", worker, events.WorktreeMoved, map[string]any{"worker": "laptop", "path": "/w", "port": float64(7901)})})
	if _, err := s.Task("task-2"); err == nil || errors.Is(err, ErrArchived) {
		t.Errorf("a broken task's error: %v", err)
	}
}

// An idea's stream is an idea, never a task: it stays out of the tasks, the
// list and the task order, so the next task's number doesn't move.
func TestAnIdeaIsNoTask(t *testing.T) {
	log := everyStream()
	for name, s := range map[string]*State{"applied": applied(log), "loaded": loaded(log)} {
		if idea, err := ideas.Get(s, "idea-1"); err != nil || idea == nil || idea.State != ideas.Accepted {
			t.Errorf("%s: idea-1 is %+v, %v; want it accepted", name, idea, err)
		}
		if task, err := s.Task("idea-1"); task != nil || err != nil {
			t.Errorf("%s: idea-1 is a task: %+v, %v", name, task, err)
		}
		if order := s.TaskIDs(); !slices.Equal(order, []string{"task-1", "task-2"}) {
			t.Errorf("%s: task order %v, want task-1 and task-2 only", name, order)
		}
		if list := idsOf(s.Tasks()); !slices.Equal(list, []string{"task-1", "task-2"}) {
			t.Errorf("%s: tasks %v, want task-1 and task-2 only", name, list)
		}
	}
}

// No roster event can make its apply panic, so a broken roster stream is
// made by running a panic through the same guard Apply uses. The roster
// stays as it was before, takes no more events, and says why; every other
// stream carries on.
func TestABrokenRosterKeepsItsLastState(t *testing.T) {
	log := everyStream()
	s := loaded(log)
	workersBefore := s.Workers()
	s.guard(events.WorkersStream, func() string { return "applying worker.exploded to workers" }, func() { panic("boom") })
	s.Apply(events.Event{Position: 60, Proposal: proposal(events.WorkersStream, system, events.WorkerDisconnected, map[string]any{"name": "laptop"})})
	s.Apply(events.Event{Position: 61, Proposal: proposal(events.AgentsStream, you, events.AgentRemoved, map[string]any{"name": "engineer"})})

	if err := s.Broken(events.WorkersStream); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("the workers stream should say why it broke, got %v", err)
	}
	if !reflect.DeepEqual(s.Workers(), workersBefore) {
		t.Errorf("a broken roster took an event: %+v", s.Workers())
	}
	if len(s.Roster()) != 0 || s.Broken(events.AgentsStream) != nil {
		t.Errorf("the agents stream should carry on: %+v", s.Roster())
	}
}

// What a getter handed out is never changed by a later Apply.
func TestWhatWasHandedOutStaysAsItWas(t *testing.T) {
	log := everyStream()
	split := len(log) - 6
	s := loaded(log[:split])
	task1, _ := s.Task("task-1")
	task2, _ := s.Task("task-2")
	roster, workerRoster, connectionRoster := s.Roster(), s.Workers(), s.Connections()
	all := s.Tasks()
	for _, e := range log[split:] {
		s.Apply(e)
	}

	before := log[:split]
	if !reflect.DeepEqual(task1, tasks.FromEvents(accepted(before, "task-1"))) || !reflect.DeepEqual(task2, tasks.FromEvents(accepted(before, "task-2"))) {
		t.Errorf("a task handed out changed:\n%+v\n%+v", task1, task2)
	}
	if !reflect.DeepEqual(all, []*tasks.Task{task1, task2}) {
		t.Errorf("the list handed out changed: %+v", all)
	}
	if !reflect.DeepEqual(roster, agents.FromEvents(accepted(before, events.AgentsStream))) ||
		!reflect.DeepEqual(workerRoster, workers.FromEvents(accepted(before, events.WorkersStream))) ||
		!reflect.DeepEqual(connectionRoster, connections.FromEvents(accepted(before, events.ConnectionsStream))) {
		t.Errorf("a roster handed out changed:\n%+v\n%+v\n%+v", roster, workerRoster, connectionRoster)
	}
	sameAsReplay(t, s, log)
}

func TestTasksAreOldestFirstInANewSlice(t *testing.T) {
	s := applied(everyStream())
	first := s.Tasks()
	if ids := idsOf(first); !slices.Equal(ids, []string{"task-1", "task-2"}) {
		t.Fatalf("Tasks: %v, want task-1 then task-2", ids)
	}
	second := s.Tasks()
	slices.Reverse(second)
	second[0] = nil
	if ids := idsOf(first); !slices.Equal(ids, []string{"task-1", "task-2"}) {
		t.Errorf("sorting one caller's slice changed another's: %v", ids)
	}
	if ids := idsOf(s.Tasks()); !slices.Equal(ids, []string{"task-1", "task-2"}) {
		t.Errorf("sorting the slice Tasks gave changed the state: %v", ids)
	}
}

// Readers call every getter while events are applied (run with -race).
func TestReadersRaceApply(t *testing.T) {
	log := everyStream()
	s := New(withIdeas, ideas.Streams())
	s.Loaded(0)
	var readers sync.WaitGroup
	done := make(chan struct{})
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				s.Position()
				if task, _ := s.Task("task-1"); task != nil {
					_ = task.Title + string(task.Status())
				}
				for _, task := range s.Tasks() {
					_ = len(task.Trail)
				}
				_ = len(s.Roster()) + len(s.Workers().Workers) + len(s.Connections().Connections)
				_ = s.Broken(events.AgentsStream)
			}
		})
	}
	for _, e := range log {
		s.Apply(e)
	}
	close(done)
	readers.Wait()
	sameAsReplay(t, s, log)
}

// applied is a state built by applying every event, from nothing.
func applied(log []events.Event) *State { return appliedWith(withIdeas, log) }

// appliedWith is applied, reading the streams as catalogue says.
func appliedWith(catalogue *events.Catalogue, log []events.Event) *State {
	return applyInto(New(catalogue, ideas.Streams()), log)
}

// applyInto applies every event of the log to s, a new state.
func applyInto(s *State, log []events.Event) *State {
	s.Loaded(0)
	for _, e := range log {
		s.Apply(e)
	}
	return s
}

func idsOf(list []*tasks.Task) []string {
	var ids []string
	for _, task := range list {
		if task == nil {
			ids = append(ids, "<nil>")
		} else {
			ids = append(ids, task.ID)
		}
	}
	return ids
}

// An idea Agentry accepted, the task it made and started, and the settings
// that turn it on, loaded at any point, give what a replay gives.
func TestAnIdeaAgentryAcceptedEqualsAReplay(t *testing.T) {
	log := append(everyStream(), logOf(
		proposal("idea-2", system, ideas.IdeaSuggested, map[string]any{"title": "Agreed", "problem": "p", "proposal": "f", "size": "S",
			"repo": "/r", "task": "task-1", "backedBy": []any{"engineer"}}),
		proposal("task-3", system, events.TaskCreated, map[string]any{"title": "Agreed", "repo": "/r", "coordinator": "tech-lead",
			"parked": "backlog", "idea": "idea-2"}),
		proposal("idea-2", system, ideas.IdeaAccepted, map[string]any{"task": "task-3", "note": "auto-accepted: all Visionaries agreed"}),
		proposal("task-3", system, events.TaskHeld, map[string]any{"place": "", "note": "", "column": "in-progress", "sentBack": false}),
		proposal(events.SettingsStream, you, events.SettingsChanged, map[string]any{settings.AutoAcceptIdeas: true, settings.AutoStartIdeas: true}),
	)...)
	for i := range log {
		log[i].Position = int64(i + 1)
	}
	for split := range len(log) + 1 {
		s := loaded(log[:split])
		for _, e := range log[split:] {
			s.Apply(e)
		}
		sameAsReplay(t, s, log)
		if t.Failed() {
			t.Fatalf("loading the first %d events", split)
		}
	}
	s := loaded(log)
	idea, _ := ideas.Get(s, "idea-2")
	task, _ := s.Task("task-3")
	if !idea.AutoAccepted || task.Parked != "" || !s.Settings().AutoAcceptIdeas || !s.Settings().AutoStartIdeas {
		t.Errorf("idea %+v, task parked %q, settings %+v", idea, task.Parked, s.Settings())
	}
}
