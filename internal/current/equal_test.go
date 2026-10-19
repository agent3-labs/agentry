package current

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/ideas"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/tasks/taskstest"
)

// openStore is a real store with no order check, so it also takes events
// the rules would refuse, such as one that breaks a task's apply.
// withIdeas is the catalogue serve opens the log with.
var withIdeas = func() *events.Catalogue {
	catalogue, err := events.NewCatalogue(ideas.Registration())
	if err != nil {
		panic(err)
	}
	return catalogue
}()

func openStore(t *testing.T, proposals ...events.Proposal) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "agentry.db"), withIdeas, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, p := range proposals {
		propose(t, s, p)
	}
	return s
}

func propose(t *testing.T, s *store.Store, p events.Proposal) {
	t.Helper()
	if _, _, err := s.Propose(p); err != nil {
		t.Fatal(err)
	}
}

// fromTheLog applies every event after the state's position, read back
// from the log: in the stored form, as a restart would load it.
func fromTheLog(t *testing.T, s *store.Store, state *State) {
	t.Helper()
	log, err := s.After(state.Position(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range log {
		state.Apply(e)
	}
}

func someProposals() []events.Proposal {
	var proposals []events.Proposal
	for _, e := range everyStream() {
		proposals = append(proposals, e.Proposal)
	}
	return append(proposals,
		proposal("task-1", you, "task.exploded", nil), // refused by the shape check
		proposal("task-3", you, events.TaskCreated, map[string]any{"title": "Third", "repo": "/r", "coordinator": "tech-lead"}),
		proposal("task-3", worker, events.WorktreeMoved, map[string]any{"worker": "laptop", "path": "/w", "port": 7901}),
	)
}

func TestEqualPassesOnAStateThatFollowedTheLog(t *testing.T) {
	s := openStore(t, someProposals()...)
	state := New(withIdeas, ideas.Streams())
	state.Loaded(0)
	fromTheLog(t, s, state)
	if err := Equal(s, state); err != nil {
		t.Errorf("applied: %v", err)
	}
	if state.Broken("task-3") == nil {
		t.Errorf("task-3 should be broken, and Equal should have accepted that its replay fails too")
	}

	loadedState := New(withIdeas, ideas.Streams())
	for _, stream := range []string{"task-3", events.AgentsStream, "task-1", events.WorkersStream, events.PolicyStream, "task-2", events.ConnectionsStream, "idea-1", events.SettingsStream} {
		accepted, err := s.Stream(stream)
		if err != nil {
			t.Fatal(err)
		}
		loadedState.Load(stream, accepted)
	}
	loadedState.Loaded(s.LastPosition())
	if err := Equal(s, loadedState); err != nil {
		t.Errorf("loaded: %v", err)
	}
}

func TestEqualNamesWhatAMissedApplyLeftBehind(t *testing.T) {
	cases := []struct {
		name   string
		missed events.Proposal
		want   string
	}{
		{"task", proposal("task-2", lead, events.TaskRenamed, map[string]any{"title": "Missed"}), "task-2: field Title"},
		{"idea", proposal("idea-1", you, ideas.IdeaDismissed, map[string]any{"note": "Missed"}), "idea-1: field State"},
		{"new task", proposal("task-4", you, events.TaskCreated, map[string]any{"title": "Missed", "repo": "/r", "coordinator": "tech-lead"}), "task-4:"},
		{"roster", proposal(events.AgentsStream, you, events.AgentLearned, map[string]any{"name": "engineer", "knowledge": "Missed."}), "agents:"},
		{"workers", proposal(events.WorkersStream, system, events.WorkerDisconnected, map[string]any{"name": "laptop"}), "workers: field Workers"},
		{"policy", proposal(events.PolicyStream, system, events.PolicyLoaded, map[string]any{"hash": "h2", "path": "/p"}), "policy: the state's last position is"},
		{"rejection", proposal("task-1", you, "task.exploded", nil), "position: the state is at"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := openStore(t, someProposals()...)
			state := New(withIdeas, ideas.Streams())
			state.Loaded(0)
			fromTheLog(t, s, state)
			propose(t, s, c.missed)
			if err := Equal(s, state); err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("Equal after a missed %s: %v, want it to start %q", c.missed.Type, err, c.want)
			}
		})
	}
}

// Equal checks each stream's last position, so it sees one an Apply left
// behind even where the stream's state is right.
func TestEqualSeesAStreamsWrongPosition(t *testing.T) {
	s := openStore(t, someProposals()...)
	state := New(withIdeas, ideas.Streams())
	state.Loaded(0)
	fromTheLog(t, s, state)
	state.streams["task-1"]--
	if err := Equal(s, state); err == nil || !strings.HasPrefix(err.Error(), "task-1: the state's last position is") {
		t.Errorf("Equal with task-1's last position wrong: %v", err)
	}
}

func TestEqualCatchesTheWrongTaskOrder(t *testing.T) {
	s := openStore(t, someProposals()...)
	state := New(withIdeas, ideas.Streams())
	state.Loaded(0)
	fromTheLog(t, s, state)
	state.created[0].id, state.created[1].id = state.created[1].id, state.created[0].id
	if err := Equal(s, state); err == nil || !strings.HasPrefix(err.Error(), "task order:") {
		t.Errorf("Equal with task-1 and task-2 swapped: %v", err)
	}
}

func TestEqualCatchesTheWrongNextTask(t *testing.T) {
	s := openStore(t, someProposals()...)
	state := New(withIdeas, ideas.Streams())
	state.Loaded(0)
	fromTheLog(t, s, state)
	state.tasksMade++
	if err := Equal(s, state); err == nil || !strings.HasPrefix(err.Error(), "next task:") {
		t.Errorf("Equal with one task.created too many counted: %v", err)
	}
}

func TestEqualCatchesTheWrongIdeaOrder(t *testing.T) {
	s := openStore(t, append(someProposals(), proposal("idea-2", system, ideas.IdeaSuggested,
		map[string]any{"title": "Later", "problem": "p", "proposal": "p", "size": "S", "repo": "/r", "task": "task-1"}))...)
	state := New(withIdeas, ideas.Streams())
	state.Loaded(0)
	fromTheLog(t, s, state)
	order := state.ownedOrder[ideas.Prefix]
	order[0].id, order[1].id = order[1].id, order[0].id
	if err := Equal(s, state); err == nil || !strings.HasPrefix(err.Error(), "idea- order:") {
		t.Errorf("Equal with idea-1 and idea-2 swapped: %v", err)
	}
}

// Equal compares every field of a task, so it sees a DoneAt the state got
// wrong though every other field is right.
func TestEqualSeesAWrongDoneAt(t *testing.T) {
	s := openStore(t, someProposals()...)
	state := New(withIdeas, ideas.Streams())
	state.Loaded(0)
	fromTheLog(t, s, state)
	wrong := state.tasks["task-1"].Clone()
	wrong.DoneAt = time.Unix(1, 0).UTC()
	state.tasks["task-1"] = wrong
	if err := Equal(s, state); err == nil || err.Error() != "task-1: field DoneAt differs from a replay" {
		t.Errorf("Equal with task-1's DoneAt wrong: %v", err)
	}
}

// Equal compares an archived task's summary with its marker's, and sees a
// summary the state lost, or a task it holds whole that the log archived.
func TestEqualSeesAWrongSummary(t *testing.T) {
	s := openStore(t, someProposals()...)
	own, err := s.Stream("task-1")
	if err != nil {
		t.Fatal(err)
	}
	propose(t, s, taskstest.Marker(own, tasks.SummaryOf(tasks.FromEvents(own), nil)))
	for _, c := range []struct {
		name   string
		change func(state *State)
		want   string
	}{
		{"a field", func(state *State) {
			summary := state.archived["task-1"]
			summary.Title = "Other"
			state.archived["task-1"] = summary
		}, "task-1: field Title differs from a replay"},
		{"the whole task", func(state *State) {
			delete(state.archived, "task-1")
			state.tasks["task-1"] = tasks.FromEvents(own)
		}, "task-1: the state differs from a replay"},
	} {
		state := New(withIdeas, ideas.Streams())
		state.Loaded(0)
		fromTheLog(t, s, state)
		c.change(state)
		if err := Equal(s, state); err == nil || err.Error() != c.want {
			t.Errorf("%s wrong: %v, want %q", c.name, err, c.want)
		}
	}
}

// heldApply is a state that, while hold is set, keeps the store waiting
// after it applies an event: the state then has an event the log's readers
// don't see yet, as it does for a moment on every write.
type heldApply struct {
	*State
	hold    bool
	applied chan struct{} // the state has the held event
	release chan struct{} // the store may go on
}

func (h *heldApply) Apply(e events.Event) {
	h.State.Apply(e)
	if h.hold {
		h.hold = false
		close(h.applied)
		<-h.release
	}
}

// The store goes on taking events while Equal runs, as the coordinator
// loop does after a test's body: Equal compares at one cut, so it finds no
// difference with a state that is ahead of the log, nor with events
// committed while it reads.
func TestEqualHoldsWhileTheStoreTakesEvents(t *testing.T) {
	state := &heldApply{State: New(withIdeas, ideas.Streams()), applied: make(chan struct{}), release: make(chan struct{})}
	s, err := store.Open(filepath.Join(t.TempDir(), "agentry.db"), withIdeas, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, p := range someProposals() {
		propose(t, s, p)
	}
	rename := func(title string) events.Proposal {
		return proposal("task-1", lead, events.TaskRenamed, map[string]any{"title": title})
	}

	state.hold = true
	held := make(chan error, 1)
	go func() {
		_, _, err := s.Propose(rename("Held"))
		held <- err
	}()
	<-state.applied
	equal := make(chan error, 1)
	go func() { equal <- Equal(s, state.State) }()
	time.Sleep(50 * time.Millisecond) // for Equal to copy the state, ahead of the log
	close(state.release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		propose(t, s, rename(fmt.Sprintf("After %d", i)))
	}
	if err := <-equal; err != nil {
		t.Errorf("Equal while the store took events: %v", err)
	}
	if err := Equal(s, state.State); err != nil {
		t.Errorf("Equal after: %v", err)
	}
}
