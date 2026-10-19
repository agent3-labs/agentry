package current

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/connections"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/exactgob"
	"gitlab.com/dev.chempa/agentry/internal/settings"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/workers"
)

// A snapshot is the state written to a file, so a start can read it and
// apply only the events after it instead of replaying the whole log. It is
// a cache: Resume throws it away whenever it might disagree with the log,
// which stays the only truth (docs/design/log-growth.md, section 2).
//
// The file is one JSON header line, then the body: chunks, each a length
// and an exactgob value, so they decode on their own and in parallel. The
// first chunk is everything but the tasks; the rest are the tasks, a run of
// them each.

// snapshotFormat changes only if the header itself does. What the body
// holds is tied to the build that wrote it, so it needs no version.
const snapshotFormat = 1

// tasksPerChunk is how many tasks a chunk holds: enough that a chunk's gob
// type descriptions cost little, few enough to share a log's tasks out
// over the cores.
const tasksPerChunk = 16

// header is a snapshot's first line, checked before the body is read.
type header struct {
	Format   int    `json:"format"`
	Build    string `json:"build"`    // the code that wrote it: a hash of its binary
	Position int64  `json:"position"` // of the last event the state holds
	Anchor   string `json:"anchor"`   // store.Hash of the event at Position, so another log is told apart
	Size     int64  `json:"size"`     // of the body
	SHA256   string `json:"sha256"`   // of the body, so a torn or corrupt file is told apart
}

// savedState is the first chunk: everything Equal compares but the tasks
// and the owned streams. A broken stream is held by name alone, and an
// owned one only by its place in the order: Resume rebuilds both from the
// log, as their streams are small, and a broken one then breaks again with
// its reason.
type savedState struct {
	Position    int64
	Streams     map[string]int64
	Created     []savedPlace
	TasksMade   int
	OwnedOrder  map[string][]savedPlace
	Broken      []string
	Archived    map[string]tasks.Summary
	Roster      agents.Saved
	Workers     workers.Saved
	Connections connections.Saved
	Settings    settings.Settings
	Prefixes    map[string]Unknown
	Types       map[events.Type]Unknown
}

// savedPlace is a created, exported for gob.
type savedPlace struct {
	Position int64
	ID       string
}

// savedTask is a task and the stream it is kept by.
type savedTask struct {
	Stream string
	Task   tasks.Saved
}

// snapshotAt is what Resume checks a restored state against the log by,
// and the streams it rebuilds from the log.
type snapshotAt struct {
	position int64
	anchor   string
	broken   []string
}

// writeSnapshot writes the state, as it is now, to w. log is the store the
// state is kept from: its event at the state's position anchors the
// snapshot. build names the code writing it.
func writeSnapshot(w io.Writer, s *State, log *store.Store, build string) error {
	return writeCopy(w, s.copy(), log, build)
}

// writeCopy is writeSnapshot of held, a copy of the state, which nothing
// else changes, so it is read with no lock held.
func writeCopy(w io.Writer, held *State, log *store.Store, build string) error {
	if held.position == 0 {
		return errors.New("an empty state has nothing to write")
	}
	if err := awaitApplied(log, held.position); err != nil {
		return err
	}
	anchor, found, err := log.Hash(held.position)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("the log has no event at the state's position, %d", held.position)
	}
	body, err := held.body()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	line, err := json.Marshal(header{Format: snapshotFormat, Build: build, Position: held.position,
		Anchor: anchor, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		return err
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// settling is how long a writer of the state waits for the log to reach it.
const settling = 5 * time.Second

// awaitApplied waits until the log's readers see every event up to
// position. The store gives the state each event before its readers see
// it, so a copy of the state can be an event or two ahead of what Hash and
// Stream answer; a state that stays ahead was built from some other log.
// writeSnapshot waits so the anchor is the event the state was copied at,
// and Equal so it reads the log to the copy's position.
func awaitApplied(s *store.Store, position int64) error {
	deadline := time.Now().Add(settling)
	for s.LastPosition() < position {
		if time.Now().After(deadline) {
			return fmt.Errorf("position: the state is at %d, the log at %d after %s", position, s.LastPosition(), settling)
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

// body is the chunks of a copy of the state, which nothing else changes.
func (s *State) body() ([]byte, error) {
	var body bytes.Buffer
	if err := writeChunk(&body, s.saved()); err != nil {
		return nil, err
	}
	// A broken task is rebuilt, not read; the map may still hold the task
	// as it was before the event that broke it.
	var streams []string
	for stream := range s.tasks {
		if s.broken[stream] == nil {
			streams = append(streams, stream)
		}
	}
	slices.Sort(streams)
	for run := range slices.Chunk(streams, tasksPerChunk) {
		chunk := make([]savedTask, len(run))
		for i, stream := range run {
			chunk[i] = savedTask{Stream: stream, Task: s.tasks[stream].Save()}
		}
		if err := writeChunk(&body, chunk); err != nil {
			return nil, err
		}
	}
	return body.Bytes(), nil
}

func (s *State) saved() savedState {
	ownedOrder := map[string][]savedPlace{}
	for prefix, order := range s.ownedOrder {
		ownedOrder[prefix] = placesOf(order)
	}
	return savedState{
		Position: s.position, Streams: s.streams, Created: placesOf(s.created), TasksMade: s.tasksMade,
		OwnedOrder: ownedOrder, Broken: slices.Sorted(maps.Keys(s.broken)), Archived: s.archived,
		Roster: s.roster.Save(), Workers: s.workers.Save(), Connections: s.connections.Save(), Settings: s.settings,
		Prefixes: s.skipped.prefixes, Types: s.skipped.types,
	}
}

func placesOf(order []created) []savedPlace {
	if order == nil {
		return nil
	}
	places := make([]savedPlace, len(order))
	for i, c := range order {
		places[i] = savedPlace{c.position, c.id}
	}
	return places
}

func createdOf(places []savedPlace) []created {
	if places == nil {
		return nil
	}
	order := make([]created, len(places))
	for i, p := range places {
		order[i] = created{p.Position, p.ID}
	}
	return order
}

// writeChunk writes v as a chunk: its length, then v.
func writeChunk(w *bytes.Buffer, v any) error {
	var chunk bytes.Buffer
	if err := exactgob.Encode(&chunk, v); err != nil {
		return err
	}
	w.Write(binary.AppendUvarint(nil, uint64(chunk.Len())))
	w.Write(chunk.Bytes())
	return nil
}

// errOtherCode is a snapshot written by another build, or in another
// format: not corrupt, only of no use to this code.
var errOtherCode = errors.New("written by other code")

// maxHeader is longer than any header: a first line longer than this is no
// header, and isn't read to its end.
const maxHeader = 4096

// readSnapshot reads a snapshot into s, a new state, for Resume to check
// against the log. An error that wraps errOtherCode means the snapshot is
// of no use here; any other means it is torn or corrupt.
func (s *State) readSnapshot(r io.Reader, build string) error {
	reader := bufio.NewReaderSize(r, maxHeader)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return fmt.Errorf("reading the header: %w", err)
	}
	var h header
	if err := json.Unmarshal(line, &h); err != nil {
		return fmt.Errorf("reading the header: %w", err)
	}
	if h.Format != snapshotFormat {
		return fmt.Errorf("%w: format %d, not %d", errOtherCode, h.Format, snapshotFormat)
	}
	if h.Build != build {
		return fmt.Errorf("%w: build %s, not %s", errOtherCode, h.Build, build)
	}
	body, err := io.ReadAll(io.LimitReader(reader, h.Size+1))
	if err != nil {
		return err
	}
	if int64(len(body)) != h.Size {
		return fmt.Errorf("the body is %d bytes, the header says %d", len(body), h.Size)
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != h.SHA256 {
		return errors.New("the body's sha256 differs from the header's")
	}
	broken, err := s.decode(body)
	if err != nil {
		return fmt.Errorf("decoding the body: %w", err)
	}
	if s.position != h.Position {
		return fmt.Errorf("the body is at position %d, the header says %d", s.position, h.Position)
	}
	s.snapshot = &snapshotAt{position: h.Position, anchor: h.Anchor, broken: broken}
	return nil
}

// decode fills s from a body's chunks, decoding and restoring the tasks in
// parallel, and returns the broken streams. A broken stream's roster or
// settings are left as New made them, as a replay leaves them, for Resume
// to rebuild.
func (s *State) decode(body []byte) (broken []string, err error) {
	chunks, err := chunksOf(body)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, errors.New("the body has no chunks")
	}
	var saved savedState
	if err := exactgob.Decode(bytes.NewReader(chunks[0]), &saved); err != nil {
		return nil, err
	}
	byStream := make([]map[string]*tasks.Task, len(chunks)-1)
	err = inParallel(len(byStream), func(i int) error {
		var chunk []savedTask
		if err := exactgob.Decode(bytes.NewReader(chunks[i+1]), &chunk); err != nil {
			return err
		}
		byStream[i] = make(map[string]*tasks.Task, len(chunk))
		for _, saved := range chunk {
			byStream[i][saved.Stream] = tasks.Restore(saved.Task)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chunk := range byStream {
		maps.Copy(s.tasks, chunk)
	}
	isBroken := map[string]bool{}
	for _, stream := range saved.Broken {
		isBroken[stream] = true
	}
	s.position, s.streams, s.created, s.tasksMade = saved.Position, saved.Streams, createdOf(saved.Created), saved.TasksMade
	for prefix, order := range saved.OwnedOrder {
		s.ownedOrder[prefix] = createdOf(order)
	}
	s.archived = saved.Archived
	if !isBroken[events.AgentsStream] {
		s.roster = agents.Restore(saved.Roster)
	}
	if !isBroken[events.WorkersStream] {
		s.workers = workers.Restore(saved.Workers)
	}
	if !isBroken[events.ConnectionsStream] {
		s.connections = connections.Restore(saved.Connections)
	}
	if !isBroken[events.SettingsStream] {
		s.settings = saved.Settings
	}
	s.skipped = tally{prefixes: saved.Prefixes, types: saved.Types}
	return saved.Broken, nil
}

// chunksOf splits a body into its chunks, without decoding them.
func chunksOf(body []byte) ([][]byte, error) {
	var chunks [][]byte
	for len(body) > 0 {
		size, n := binary.Uvarint(body)
		if n <= 0 || size > uint64(len(body)-n) {
			return nil, errors.New("a chunk runs past the end of the body")
		}
		chunks = append(chunks, body[n:n+int(size)])
		body = body[n+int(size):]
	}
	return chunks, nil
}

// inParallel runs do for 0 to n-1 on as many goroutines as there are
// cores, and returns every error.
func inParallel(n int, do func(i int) error) error {
	errs := make([]error, n)
	var next atomic.Int64
	var running sync.WaitGroup
	for range min(n, runtime.GOMAXPROCS(0)) {
		running.Go(func() {
			for i := int(next.Add(1) - 1); i < n; i = int(next.Add(1) - 1) {
				errs[i] = do(i)
			}
		})
	}
	running.Wait()
	return errors.Join(errs...)
}
