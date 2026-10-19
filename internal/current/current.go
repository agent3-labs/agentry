// Package current keeps the state of every stream in memory, as of the
// last event the log committed. The log stays the only truth: the state is
// built from it at every start and changed only by the store, as each event
// commits, so it can't disagree with what happened. A start may read the
// state from a snapshot instead and apply only the events after it, but a
// snapshot is only a cache, thrown away whenever it might disagree with
// the log (snapshot.go).
//
// The catalogue the state is made with says what each stream is. A task's
// stream makes a task, and a feature's own stream whatever that feature's
// Streams build from it: an idea, never a task. The agents, workers and
// connections streams each make a roster, and the settings stream your
// settings. The policy stream keeps no state here: its
// events only move the position. A foreign stream, which a feature this
// build doesn't have wrote, is skipped and only counted, as is an event of
// a type the catalogue doesn't know in any other stream.
package current

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/connections"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/features"
	"gitlab.com/dev.chempa/agentry/internal/settings"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/workers"
)

// State is every stream's state. A *tasks.Task or a roster it hands out is
// never changed afterwards: applying an event replaces it with a new one,
// so readers may keep what they got as long as they like, without a lock.
// They must not change it themselves.
type State struct {
	mu          sync.RWMutex
	catalogue   *events.Catalogue
	position    int64                        // of the last event applied, rejections included
	streams     map[string]int64             // each stream's last accepted event applied, by stream
	tasks       map[string]*tasks.Task       // by stream
	created     []created                    // every task.created applied, in the order of their positions, as the log lists them
	tasksMade   int                          // every task.created applied, a foreign stream's too, as Store.Create counts them
	owners      map[string]*features.Streams // by prefix: the features whose streams the state keeps
	owned       map[string]any               // a feature's stream's state, as its Streams built it, by stream
	ownedOrder  map[string][]created         // by prefix: each owned stream's first event applied, in the order of their positions
	broken      map[string]error             // streams whose apply panicked: reads fail, as a replay would
	archived    map[string]tasks.Summary     // tasks the state holds as their summary alone, by stream
	roster      agents.Roster
	workers     workers.Roster
	connections connections.Roster
	settings    settings.Settings
	skipped     tally // foreign streams and unknown types

	snapshot *snapshotAt   // read from a snapshot, still to be checked against the log by Resume
	tail     *tail         // set while Resume applies the events after a snapshot
	resumed  int64         // the snapshot position Open went on from, 0 after a whole replay
	moved    chan struct{} // sent on, if nothing waits in it, when the position moves: a SnapshotWriter's wake
}

var _ features.Reader = (*State)(nil)

// created is a task.created event's place in the log, or the first event of
// a feature's stream. The task and owned stream orders are by these
// positions, not by when the state took each event, so a state that starts
// part way and takes the rest keeps the log's order.
type created struct {
	position int64
	id       string
}

// ErrArchived is Task's error for an archived task: a done task the state
// holds no full state for, which takes no more events. Its readers skip it
// as they skip a done task, without reading its stream.
var ErrArchived = errors.New("the task is archived")

// New is an empty state that reads streams as catalogue says, which must be
// the catalogue the store is opened with, and keeps the streams of each
// feature in owned. A stream the catalogue doesn't give to a feature is
// never one of owned's, whatever its name: without the registration it is
// foreign. An owned stream with no Streams here keeps no state.
func New(catalogue *events.Catalogue, owned ...*features.Streams) *State {
	owners := map[string]*features.Streams{}
	for _, streams := range owned {
		if owners[streams.Prefix] != nil {
			panic("current: two features own the streams " + streams.Prefix)
		}
		owners[streams.Prefix] = streams
	}
	s := &State{catalogue: catalogue, owners: owners, moved: make(chan struct{}, 1)}
	s.empty()
	return s
}

// empty sets everything the log fills back to nothing, as New makes it:
// for New, and for Resume to throw a snapshot away. A field the log fills
// must be set here, even to its zero value, or a thrown away snapshot
// leaves it behind.
func (s *State) empty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.position, s.streams, s.tasks = 0, map[string]int64{}, map[string]*tasks.Task{}
	s.created, s.tasksMade = nil, 0
	s.owned, s.ownedOrder = map[string]any{}, map[string][]created{}
	s.broken, s.archived = map[string]error{}, map[string]tasks.Summary{}
	s.roster, s.workers, s.connections, s.settings = nil, workers.Roster{}, connections.Roster{}, settings.Default()
	s.skipped = newTally()
	s.snapshot, s.tail, s.resumed = nil, nil, 0
}

// ownerOf is the Streams that keeps a stream, or nil for a stream no
// feature here owns.
func (s *State) ownerOf(stream string, kind events.StreamKind) *features.Streams {
	if kind != events.OwnedStream {
		return nil
	}
	for prefix, streams := range s.owners {
		if strings.HasPrefix(stream, prefix) {
			return streams
		}
	}
	return nil
}

// Load builds a stream's state from its accepted events, in place: no copy
// per event. The store calls it at Open, once per stream.
func (s *State) Load(stream string, accepted []events.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(accepted) > 0 {
		s.streams[stream] = accepted[len(accepted)-1].Position
	}
	kind := s.catalogue.KindOf(stream)
	for i, e := range accepted {
		s.skipped.count(s.catalogue, kind, e, i == 0)
		s.countMade(e, i == 0)
	}
	if kind == events.ForeignStream {
		return
	}
	for i, e := range accepted {
		s.made(e, kind, i == 0)
	}
	s.fold(stream, accepted)
}

// fold builds a stream's state from its accepted events, as a replay does,
// and keeps it, or marks the stream broken if it can't be built. A foreign
// stream never gets here.
func (s *State) fold(stream string, accepted []events.Event) {
	kind := s.catalogue.KindOf(stream)
	owner := s.ownerOf(stream, kind)
	s.guard(stream, func() string { return "building " + stream }, func() {
		switch stream {
		case events.AgentsStream:
			s.roster = agents.FromEvents(accepted)
		case events.WorkersStream:
			s.workers = workers.FromEvents(accepted)
		case events.ConnectionsStream:
			s.connections = connections.FromEvents(accepted)
		case events.SettingsStream:
			s.settings = settings.FromEvents(accepted)
		case events.PolicyStream:
		default:
			if owner != nil {
				if held := owner.FromEvents(accepted); held != nil {
					s.owned[stream] = held
				}
			} else if kind != events.TaskStream {
				return
			} else if task, err := tasks.Replay(accepted); err != nil {
				s.breaks(stream, err)
			} else if task != nil {
				s.tasks[stream] = task
			} else if summary, archived := tasks.Archived(accepted); archived {
				s.archived[stream] = summary
			}
		}
	})
}

// Refold builds a stream's state again from its accepted events before e
// and e, as Load does, for an event that changes what came before it
// (events.Catalogue.Refolds). What the state held for the stream goes, even
// a broken or archived task, and the position moves as Apply moves it.
// The fold keeps the summary of a task still archived, so task.unarchived
// brings the task back from its events, and a future event.redacted on an
// archived stream leaves it archived.
func (s *State) Refold(stream string, before []events.Event, e events.Event) {
	defer s.wake()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.position = e.Position
	kind := s.catalogue.KindOf(stream)
	first := s.streams[stream] == 0
	s.skipped.count(s.catalogue, kind, e, first)
	s.streams[stream] = e.Position
	s.countMade(e, first)
	if kind == events.ForeignStream {
		return
	}
	s.made(e, kind, first)
	delete(s.tasks, stream)
	delete(s.owned, stream)
	delete(s.broken, stream)
	delete(s.archived, stream)
	s.fold(stream, append(slices.Clip(before), e))
}

// Loaded ends the load: position is the log's last, rejections included.
func (s *State) Loaded(position int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slices.SortFunc(s.created, byPosition)
	for _, order := range s.ownedOrder {
		slices.SortFunc(order, byPosition)
	}
	s.position = position
}

func byPosition(a, b created) int { return cmp.Compare(a.position, b.position) }

// wake tells a SnapshotWriter the position moved. It never waits: a wake
// not yet taken already says so.
func (s *State) wake() {
	select {
	case s.moved <- struct{}{}:
	default:
	}
}

// Since is the last position the state holds, for the store to apply the
// rest of the log from: 0 for a new state.
func (s *State) Since() int64 { return s.Position() }

// Apply changes the state by one committed event. A proposal.rejected only
// moves the position, as Store.Stream leaves it out: it never happened.
// A foreign stream's event is only counted. A broken stream takes no more
// events, as a replay would fail before them.
func (s *State) Apply(e events.Event) {
	defer s.wake()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.position = e.Position
	if e.Type == events.ProposalRejected {
		return
	}
	kind := s.catalogue.KindOf(e.Stream)
	first := s.streams[e.Stream] == 0
	s.skipped.count(s.catalogue, kind, e, first)
	s.streams[e.Stream] = e.Position
	s.countMade(e, first)
	if kind == events.ForeignStream {
		return
	}
	s.made(e, kind, first)
	if s.broken[e.Stream] != nil {
		return
	}
	// Said as tasks.Replay says it, so a task that breaks here reads as one
	// that broke at Load.
	applying := func() string { return fmt.Sprintf("applying %s to %s at %d", e.Type, e.Stream, e.Position) }
	owner := s.ownerOf(e.Stream, kind)
	s.guard(e.Stream, applying, func() {
		switch e.Stream {
		case events.AgentsStream:
			s.roster = s.roster.Next(e)
		case events.WorkersStream:
			s.workers = s.workers.Next(e)
		case events.ConnectionsStream:
			s.connections = s.connections.Next(e)
		case events.SettingsStream:
			s.settings = s.settings.Next(e)
		case events.PolicyStream:
		default:
			if owner != nil {
				if held := owner.Next(s.owned[e.Stream], e); held != nil {
					s.owned[e.Stream] = held
				}
			} else if kind != events.TaskStream {
				return
			} else if e.Type == events.TaskArchived {
				s.archive(e)
			} else if task := s.nextTask(s.tasks[e.Stream], e); task != nil {
				s.tasks[e.Stream] = task
			}
		}
	})
}

// archive keeps a task as its marker's summary alone, as a replay of its
// stream ends: tasks.Replay folds it to nil, and tasks.Archived gives the
// summary.
func (s *State) archive(marker events.Event) {
	summary, _ := tasks.Archived([]events.Event{marker})
	delete(s.tasks, marker.Stream)
	s.archived[marker.Stream] = summary
}

// nextTask is tasks.Next, or, while Resume applies the events after a
// snapshot, the same change made in place: nobody has read the restored
// tasks yet, and copying a large task for every event would cost more than
// the rest of the load. A panic part way through leaves the task half
// changed, but Resume then throws the whole state away.
func (s *State) nextTask(task *tasks.Task, e events.Event) *tasks.Task {
	if s.tail == nil || task == nil || e.Type == events.TaskCreated {
		return tasks.Next(task, e)
	}
	task.Apply(e)
	return task
}

// made notes where a task.created, or the first event of a stream a
// feature here owns, is in the log, for the task and owned stream orders.
// An archived task's task.created row is gone with the rest, so a marker
// that starts its stream stands for it, at the position it names.
//
// The store applies in the log's order, after everything Load or an
// earlier Apply gave, so appending a task.created keeps the positions in
// order. A marker's task.created comes before it, so it goes in its place;
// during Load the order is anyone's until Loaded sorts it.
func (s *State) made(e events.Event, kind events.StreamKind, first bool) {
	switch {
	case e.Type == events.TaskCreated:
		s.created = append(s.created, created{e.Position, e.Stream})
	case e.Type == events.TaskArchived && first:
		c := created{int64(e.Number("created")), e.Stream}
		at, _ := slices.BinarySearchFunc(s.created, c, byPosition)
		s.created = slices.Insert(s.created, at, c)
	}
	if owner := s.ownerOf(e.Stream, kind); owner != nil && first {
		s.ownedOrder[owner.Prefix] = append(s.ownedOrder[owner.Prefix], created{e.Position, e.Stream})
	}
}

// countMade counts a task.created in any stream, and a marker that stands
// for one (see made), so NextTask names the stream Store.Create would.
func (s *State) countMade(e events.Event, first bool) {
	if e.Type == events.TaskCreated || e.Type == events.TaskArchived && first {
		s.tasksMade++
	}
}

// guard runs change, and marks the stream broken if it panics, so one bad
// stream fails its own reads and leaves every other stream working. doing
// says what change was doing, for the error.
func (s *State) guard(stream string, doing func() string, change func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.breaks(stream, fmt.Errorf("%s: %v", doing(), recovered))
		}
	}()
	change()
}

// breaks marks a stream broken, and logs it: a broken task leaves the lists,
// and the log line is where you find out why.
func (s *State) breaks(stream string, err error) {
	s.broken[stream] = err
	if s.tail != nil && s.tail.broke == nil {
		s.tail.broke = err
	}
	slog.Error("current: a stream's state can't be built, so its reads fail", "stream", stream, "err", err)
}

// copy is the state as it is now, in maps of its own, so it can be read
// while the store goes on applying events to s. The tasks, owned streams'
// states and rosters in it are shared: no one changes them.
func (s *State) copy() *State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ownedOrder := map[string][]created{}
	for prefix, order := range s.ownedOrder {
		ownedOrder[prefix] = slices.Clone(order)
	}
	return &State{
		position:    s.position,
		streams:     maps.Clone(s.streams),
		tasks:       maps.Clone(s.tasks),
		created:     slices.Clone(s.created),
		tasksMade:   s.tasksMade,
		owners:      s.owners,
		owned:       maps.Clone(s.owned),
		ownedOrder:  ownedOrder,
		broken:      maps.Clone(s.broken),
		archived:    maps.Clone(s.archived),
		roster:      s.roster,
		workers:     s.workers,
		connections: s.connections,
		settings:    s.settings,
		skipped:     s.skipped.clone(),
	}
}

// Resumed is the position of the snapshot Open went on from, or 0 when it
// replayed the whole log.
func (s *State) Resumed() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resumed
}

// Position is the last event applied, rejections included.
func (s *State) Position() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.position
}

// StreamPosition is the position of the last accepted event applied to a
// stream, 0 for a stream with none.
func (s *State) StreamPosition(stream string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.streams[stream]
}

// Task is a task's state: nil, nil when there is no such task, ErrArchived
// when it is archived, and the error when its stream is broken.
func (s *State) Task(id string) (*tasks.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, archived := s.archived[id]; archived {
		return nil, ErrArchived
	}
	if err := s.broken[id]; err != nil {
		return nil, err
	}
	return s.tasks[id], nil
}

// Owned is a feature's stream's state, as its Streams left it: nil, nil
// when the stream holds nothing, and the error when it is broken. The
// feature's own typed read (ideas.Get) says what it is.
func (s *State) Owned(stream string) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.broken[stream]; err != nil {
		return nil, err
	}
	return s.owned[stream], nil
}

// Tasks is every task, oldest first, in a new slice the caller may sort.
// A broken or archived task is left out, as the list skips a task it can't
// read.
func (s *State) Tasks() []*tasks.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*tasks.Task, 0, len(s.created))
	for _, c := range s.created {
		if task := s.tasks[c.id]; task != nil && s.broken[c.id] == nil {
			all = append(all, task)
		}
	}
	return all
}

// TaskIDs is the stream of every task.created applied, oldest first, in a
// new slice: a broken or archived task's too, so its length counts every
// task made.
func (s *State) TaskIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, len(s.created))
	for i, c := range s.created {
		ids[i] = c.id
	}
	return ids
}

// NextTask is the stream the next task.created goes into: the one
// Store.Create("task-", ...) names, as both count every task.created, an
// archived task's through its marker (see Store.made).
func (s *State) NextTask() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return events.Numbered(events.TaskPrefix, s.tasksMade)
}

// OwnedIDs is every stream with prefix that a feature here owns, in the
// order of their first events in the log, in a new slice: a broken one's
// too, as TaskIDs lists a broken task.
func (s *State) OwnedIDs(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	order := s.ownedOrder[prefix]
	ids := make([]string, len(order))
	for i, c := range order {
		ids[i] = c.id
	}
	return ids
}

// Summaries is every task as the list shows it, oldest first, in a new
// slice the caller may sort: a whole task's summary, as tasks.SummaryOf
// makes it with the roster, and an archived one's, as its marker keeps it.
// A broken task is left out, as Tasks leaves it out. BlockedBy and Queued
// need the other tasks, so they are the caller's to fill.
func (s *State) Summaries() []tasks.Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]tasks.Summary, 0, len(s.created))
	for _, c := range s.created {
		if summary, archived := s.archived[c.id]; archived {
			all = append(all, summary)
		} else if task := s.tasks[c.id]; task != nil && s.broken[c.id] == nil {
			all = append(all, tasks.SummaryOf(task, s.roster))
		}
	}
	return all
}

// Roster is every agent. If the agents stream is broken, it is the roster
// as it was before the event that broke it, or empty if it broke while
// loading, and Broken says why.
func (s *State) Roster() agents.Roster {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.roster
}

// Workers is every worker; a broken stream is kept as Roster's is.
func (s *State) Workers() workers.Roster {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workers
}

// Connections is every connection; a broken stream is kept as Roster's is.
func (s *State) Connections() connections.Roster {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connections
}

// Settings are your settings; a broken stream is kept as Roster's is.
func (s *State) Settings() settings.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Unknown is what the state skipped or didn't understand: each foreign
// stream prefix's events, then each unknown type's in the other streams.
func (s *State) Unknown() []Unknown {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.skipped.list()
}

// Broken is why a stream's state could not be built, or nil.
func (s *State) Broken(stream string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.broken[stream]
}
