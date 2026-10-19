package current

import (
	"cmp"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/connections"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/features"
	"gitlab.com/dev.chempa/agentry/internal/settings"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/workers"
)

// Equal checks the state against a replay of the store's log, which stays
// the definition of state: every task, owned stream and roster, the task
// and owned stream orders, the next task's stream, each stream's last position and the
// log's, and what was skipped: foreign streams are compared by that count
// alone. It returns the first difference, named by stream, or nil. A stream
// the state holds as broken must fail to replay too.
//
// The store may go on taking events while Equal runs, so it compares at one
// cut: a copy of the state, and the log read only up to the copy's position.
// The model has each event before the log's readers do, so the copy can be
// ahead of the log for a moment; Equal waits for the log to catch up. A
// state behind the log missed an event: the cut is then the log's position.
func Equal(s *store.Store, state *State) error {
	applied := s.LastPosition()
	held := state.copy()
	cut := max(held.position, applied)
	if err := awaitApplied(s, cut); err != nil {
		return err
	}
	index, err := indexOf(s, held, cut)
	if err != nil {
		return err
	}
	for _, id := range index.tasks {
		if err := equalTask(s, cut, held, id); err != nil {
			return err
		}
	}
	if err := equalOwned(s, cut, held, index.owned); err != nil {
		return err
	}
	rosters := []struct {
		stream string
		replay func([]events.Event) (any, error)
		held   any
	}{
		{events.AgentsStream, func(e []events.Event) (any, error) { return agents.FromEvents(e), nil }, held.Roster()},
		{events.WorkersStream, func(e []events.Event) (any, error) { return workers.FromEvents(e), nil }, held.Workers()},
		{events.ConnectionsStream, func(e []events.Event) (any, error) { return connections.FromEvents(e), nil }, held.Connections()},
		{events.SettingsStream, func(e []events.Event) (any, error) { return settings.FromEvents(e), nil }, held.Settings()},
	}
	for _, r := range rosters {
		heldRoster := func() (any, error) { return r.held, held.Broken(r.stream) }
		if err := equalStream(s, cut, r.stream, r.replay, heldRoster); err != nil {
			return err
		}
	}
	if order := held.TaskIDs(); !slices.Equal(order, index.tasks) {
		return fmt.Errorf("task order: the state has %v, the log %v", order, index.tasks)
	}
	if next, inLog := held.NextTask(), events.Numbered(events.TaskPrefix, index.made); next != inLog {
		return fmt.Errorf("next task: the state names %s, the log %s", next, inLog)
	}
	if err := equalStreamPositions(held, index.streams); err != nil {
		return err
	}
	if unknown, inLog := held.skipped.list(), index.skipped.list(); !reflect.DeepEqual(unknown, inLog) {
		return fmt.Errorf("unknown: the state counts %+v, the log %+v", unknown, inLog)
	}
	if held.position != index.last {
		return fmt.Errorf("position: the state is at %d, the log at %d", held.position, index.last)
	}
	return nil
}

// equalStream compares what the state holds for a stream with a replay of
// the stream's accepted events in the log.
func equalStream[T any](s *store.Store, cut int64, stream string, replay func([]events.Event) (T, error), held func() (any, error)) error {
	accepted, err := s.Stream(stream)
	if err != nil {
		return err
	}
	return equalReplay(stream, upTo(accepted, cut), replay, held)
}

// equalTask compares a task with a replay of its stream: the whole task,
// and the summary an archived one keeps, against tasks.Archived of the
// replay. A broken task has neither, so the first compare is all there is.
func equalTask(s *store.Store, cut int64, held *State, id string) error {
	summary, archived := held.archived[id]
	whole := func() (any, error) {
		if archived {
			return (*tasks.Task)(nil), nil // its replay folds to nil too
		}
		return held.Task(id)
	}
	if err := equalStream(s, cut, id, tasks.Replay, whole); err != nil || held.broken[id] != nil {
		return err
	}
	kept := func() (any, error) {
		if archived {
			return &summary, nil
		}
		return (*tasks.Summary)(nil), nil
	}
	return equalStream(s, cut, id, archivedIn, kept)
}

// archivedIn is tasks.Archived in the shape of tasks.Replay: nil for a task
// that isn't archived.
func archivedIn(accepted []events.Event) (*tasks.Summary, error) {
	if summary, archived := tasks.Archived(accepted); archived {
		return &summary, nil
	}
	return nil, nil
}

// upTo is the events, in the log's order, at or before position.
func upTo(log []events.Event, position int64) []events.Event {
	end, _ := slices.BinarySearchFunc(log, position+1, func(e events.Event, p int64) int { return cmp.Compare(e.Position, p) })
	return log[:end]
}

// equalReplay compares what the state holds for a stream with a replay of
// accepted, the stream's accepted events.
func equalReplay[T any](stream string, accepted []events.Event, replay func([]events.Event) (T, error), held func() (any, error)) error {
	want, replayErr := replayed(replay, accepted)
	got, heldErr := held()
	switch {
	case replayErr != nil && heldErr != nil:
		return nil
	case replayErr != nil:
		return fmt.Errorf("%s: the replay fails (%v), the state doesn't", stream, replayErr)
	case heldErr != nil:
		return fmt.Errorf("%s: the state is broken (%v), the replay isn't", stream, heldErr)
	}
	if !reflect.DeepEqual(got, any(want)) {
		return fmt.Errorf("%s: %s differs from a replay", stream, differingField(got, want))
	}
	return nil
}

// replayed runs a replay, turning a panic into an error as the state does.
// A task's replay gives its own error, as tasks.Replay recovers the panic.
func replayed[T any](replay func([]events.Event) (T, error), accepted []events.Event) (state T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return replay(accepted)
}

// equalOwned compares every stream each feature's Streams keep, and their
// order, with a replay through that feature's FromEvents. inLog is each
// prefix's streams in the order of their first events in the log.
func equalOwned(s *store.Store, cut int64, held *State, inLog map[string][]string) error {
	for _, prefix := range slices.Sorted(maps.Keys(held.owners)) {
		ids := inLog[prefix]
		for _, id := range ids {
			if err := equalStream(s, cut, id, infallible(held.owners[prefix].FromEvents), func() (any, error) { return held.Owned(id) }); err != nil {
				return err
			}
		}
		if order := held.OwnedIDs(prefix); !slices.Equal(order, ids) {
			return fmt.Errorf("%s order: the state has %v, the log %v", prefix, order, ids)
		}
	}
	return nil
}

// infallible is a FromEvents in the shape of tasks.Replay: its failure is
// a panic, which replayed recovers.
func infallible[T any](fromEvents func([]events.Event) T) func([]events.Event) (T, error) {
	return func(accepted []events.Event) (T, error) { return fromEvents(accepted), nil }
}

// differingField names the first exported field that differs, when both
// are structs or pointers to them; otherwise the whole value.
func differingField(got, want any) string {
	a, b := reflect.Indirect(reflect.ValueOf(got)), reflect.Indirect(reflect.ValueOf(want))
	if !a.IsValid() || !b.IsValid() || a.Kind() != reflect.Struct || a.Type() != b.Type() {
		return "the state"
	}
	for i := range a.NumField() {
		field := a.Type().Field(i)
		if field.IsExported() && !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
			return "field " + field.Name
		}
	}
	return "an unexported field"
}

// index is the log up to a cut, as Equal compares it.
type index struct {
	tasks   []string            // the stream of every accepted task.created, oldest first, in any stream but a foreign one, and of every marker that stands for one
	made    int                 // every accepted task.created, a foreign stream's too, and every marker that stands for one
	owned   map[string][]string // by prefix: each owned stream with a Streams, in the order of its first accepted event
	streams map[string]int64    // each stream's last accepted position
	skipped tally               // foreign streams and unknown types
	last    int64               // the last position, rejections included
}

// indexOf reads the whole log up to cut, finding owned streams as held does.
func indexOf(s *store.Store, held *State, cut int64) (index, error) {
	const page = 1000
	found := index{streams: map[string]int64{}, owned: map[string][]string{}, skipped: newTally()}
	var made []created
	catalogue := s.Catalogue()
	for position := int64(0); ; {
		batch, err := s.After(position, page)
		if err != nil {
			return index{}, err
		}
		batch = upTo(batch, cut)
		for _, e := range batch {
			found.last = e.Position
			if e.Type == events.ProposalRejected {
				continue
			}
			kind := catalogue.KindOf(e.Stream)
			first := found.streams[e.Stream] == 0
			found.skipped.count(catalogue, kind, e, first)
			found.streams[e.Stream] = e.Position
			// A marker that starts its stream stands for the task.created
			// archived with the rest, at the position it names.
			standsIn := e.Type == events.TaskArchived && first
			if e.Type == events.TaskCreated || standsIn {
				found.made++
			}
			if kind == events.ForeignStream {
				continue
			}
			switch {
			case e.Type == events.TaskCreated:
				made = append(made, created{e.Position, e.Stream})
			case standsIn:
				made = append(made, created{int64(e.Number("created")), e.Stream})
			}
			if owner := held.ownerOf(e.Stream, kind); owner != nil && first {
				found.owned[owner.Prefix] = append(found.owned[owner.Prefix], e.Stream)
			}
		}
		if len(batch) < page {
			slices.SortStableFunc(made, byPosition)
			for _, c := range made {
				found.tasks = append(found.tasks, c.id)
			}
			return found, nil
		}
		position = batch[len(batch)-1].Position
	}
}

// equalStreamPositions compares each stream's last position in the state
// with the log's, naming the first stream, in order, that differs.
func equalStreamPositions(state *State, inLog map[string]int64) error {
	state.mu.RLock()
	defer state.mu.RUnlock()
	streams := slices.Concat(slices.Collect(maps.Keys(state.streams)), slices.Collect(maps.Keys(inLog)))
	slices.Sort(streams)
	for _, stream := range slices.Compact(streams) {
		if held, last := state.streams[stream], inLog[stream]; held != last {
			return fmt.Errorf("%s: the state's last position is %d, the log's %d", stream, held, last)
		}
	}
	return nil
}

// EqualThroughSnapshots checks snapshots as Equal checks the state: for
// each cut, the state of the log at path up to it is written to a snapshot
// in dir, restored and opened as serve opens it, and must equal a replay.
// It must have gone on from the snapshot, unless a stream breaks after the
// cut: Resume then replays the whole log, as it can't tell a stream the log
// breaks from one the snapshot does. The log is opened with catalogue and
// the state keeps owned, as the store being checked does. It returns the
// first difference, with its cut.
func EqualThroughSnapshots(path string, catalogue *events.Catalogue, dir string, cuts []int64, owned ...*features.Streams) error {
	const build = "equal"
	log, err := store.Open(path, catalogue, nil, nil)
	if err != nil {
		return err
	}
	defer log.Close()
	all, err := everyEvent(log)
	if err != nil {
		return err
	}
	snapshot := filepath.Join(dir, "state.snapshot")
	for _, cut := range cuts {
		state := New(catalogue, owned...)
		state.loadAll(upTo(all, cut))
		if err := WriteSnapshot(snapshot, state, log, build); err != nil {
			return fmt.Errorf("at %d: writing the snapshot: %w", cut, err)
		}
		restored := Restore(snapshot, build, catalogue, owned...)
		opened, err := store.Open(path, catalogue, restored, nil)
		if err != nil {
			return fmt.Errorf("at %d: %w", cut, err)
		}
		err = Equal(opened, restored)
		opened.Close()
		switch {
		case err != nil:
			return fmt.Errorf("at %d: %w", cut, err)
		case restored.resumed != cut && !breaksAfter(state, restored):
			return fmt.Errorf("at %d: Open replayed the whole log instead of going on from the snapshot", cut)
		}
	}
	return nil
}

// breaksAfter is whether a stream whole in before is broken in after.
func breaksAfter(before, after *State) bool {
	for stream := range after.broken {
		if before.broken[stream] == nil {
			return true
		}
	}
	return false
}

// everyEvent is the whole log, rejections included.
func everyEvent(s *store.Store) ([]events.Event, error) {
	var all []events.Event
	for {
		var after int64
		if len(all) > 0 {
			after = all[len(all)-1].Position
		}
		page, err := s.After(after, 1000)
		if err != nil || len(page) == 0 {
			return all, err
		}
		all = append(all, page...)
	}
}

// loadAll loads a log into s, a new state, as Open loads a whole log.
func (s *State) loadAll(log []events.Event) {
	var streams []string
	byStream := map[string][]events.Event{}
	for _, e := range log {
		if e.Type == events.ProposalRejected {
			continue
		}
		if _, seen := byStream[e.Stream]; !seen {
			streams = append(streams, e.Stream)
		}
		byStream[e.Stream] = append(byStream[e.Stream], e)
	}
	for _, stream := range streams {
		s.Load(stream, byStream[stream])
	}
	var last int64
	if len(log) > 0 {
		last = log[len(log)-1].Position
	}
	s.Loaded(last)
}
