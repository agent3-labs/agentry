package current

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/ideas"
	"gitlab.com/dev.chempa/agentry/internal/settings"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/tasks/taskstest"
)

// fixtureLogs are the logs every snapshot test runs over: every kind of
// stream, a task broken part way with events on it after the break, ideas,
// every event a task applies, and a story that finishes while its epic's
// coordinator decides, which only the task's unexported storyNews carries
// to the event after it.
func fixtureLogs() map[string][]events.Proposal {
	brokenThenMore := append(someProposals(),
		proposal("task-1", lead, events.TaskRenamed, map[string]any{"title": "Still applied"}),
		proposal("task-3", lead, events.TaskRenamed, map[string]any{"title": "Never applied"}),
		proposal(events.AgentsStream, you, events.AgentLearned, map[string]any{"name": "engineer", "knowledge": "After the break."}),
	)
	agreed := append(proposalsOf(everyStream()),
		proposal("idea-2", system, ideas.IdeaSuggested, map[string]any{"title": "Agreed", "problem": "p", "proposal": "f", "size": "S",
			"repo": "/r", "task": "task-1", "backedBy": []any{"engineer"}}),
		proposal("task-3", system, events.TaskCreated, map[string]any{"title": "Agreed", "repo": "/r", "coordinator": "tech-lead",
			"parked": "backlog", "idea": "idea-2"}),
		proposal("idea-2", system, ideas.IdeaAccepted, map[string]any{"task": "task-3", "note": "auto-accepted: all Visionaries agreed"}),
		proposal(events.SettingsStream, you, events.SettingsChanged, map[string]any{settings.AutoAcceptIdeas: true, settings.AutoStartIdeas: true}),
	)
	return map[string][]events.Proposal{
		"every stream":     someProposals(),
		"broken then more": brokenThenMore,
		"ideas":            agreed,
		"every task event": proposalsOf(taskstest.EveryEvent()),
		"story news":       storyNews(),
	}
}

func proposalsOf(log []events.Event) []events.Proposal {
	var proposals []events.Proposal
	for _, e := range log {
		proposals = append(proposals, e.Proposal)
	}
	return proposals
}

// storyNews is an epic whose story finishes while its coordinator's turn is
// open: the epic's next reply hands the news back to the coordinator
// (internal/tasks/apply.go, StoryDone). Then the story is removed.
func storyNews() []events.Proposal {
	started := proposal("task-1", worker, events.TurnStarted, map[string]any{"agent": "tech-lead", "hash": "h"})
	started.Correlation = "turn-1"
	return []events.Proposal{
		proposal(events.AgentsStream, system, events.AgentCreated, map[string]any{"name": "tech-lead", "prompt": "Lead.", "engine": "claude", "coordinates": true}),
		proposal("task-1", you, events.TaskCreated, map[string]any{"title": "Big", "repo": "/r", "coordinator": "tech-lead", "kind": "epic"}),
		proposal("task-1", lead, events.TaskSplit, map[string]any{"stories": []any{"task-2"}, "titles": "a", "toYou": "Splitting."}),
		started,
		proposal("task-1", system, events.StoryDone, map[string]any{"story": "task-2", "summary": "a done", "coordinator": "tech-lead"}),
		proposal("task-1", lead, events.EpicWaits, map[string]any{"note": "waiting"}),
		// Its only story removed, the epic's Stories is empty, not nil: gob
		// alone reads it back as nil.
		proposal("task-1", you, events.StoryRemoved, map[string]any{"story": "task-2"}),
	}
}

// Equivalence test 1: at every position of every fixture log, the state up
// to it, written to a snapshot, restored and opened as serve opens it,
// goes on from the snapshot and equals a replay of the whole log.
func TestASnapshotAtEveryPositionEqualsAReplay(t *testing.T) {
	for name, proposals := range fixtureLogs() {
		path, log := seededLog(t, proposals...)
		var cuts []int64
		for _, e := range log {
			cuts = append(cuts, e.Position)
		}
		if err := EqualThroughSnapshots(path, withIdeas, t.TempDir(), cuts, ideas.Streams()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The story news fixture does what it is for: the news reaches the
// coordinator only through storyNews, and the epic's stories end empty.
func TestTheStoryNewsFixtureNeedsStoryNews(t *testing.T) {
	_, log := seededLog(t, storyNews()...)
	epic, err := loaded(log).Task("task-1")
	if err != nil {
		t.Fatal(err)
	}
	heard := slices.ContainsFunc(epic.Trail, func(step tasks.Step) bool {
		return strings.Contains(step.Text, "A story finished while you were deciding")
	})
	if !heard || epic.Stories == nil || len(epic.Stories) > 0 {
		t.Errorf("the epic should hear the story's news and end with no stories, not nil ones: %+v", epic)
	}
}

// snapshotted writes a snapshot of the state of the log up to cut, beside
// the log, and returns its path.
func snapshotted(t *testing.T, logPath string, cut int64, change func(*State)) string {
	t.Helper()
	log, err := store.Open(logPath, withIdeas, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	all, err := everyEvent(log)
	if err != nil {
		t.Fatal(err)
	}
	state := New(withIdeas, ideas.Streams())
	state.loadAll(upTo(all, cut))
	if change != nil {
		change(state)
	}
	path := filepath.Join(filepath.Dir(logPath), "state.snapshot")
	if err := WriteSnapshot(path, state, log, "build-1"); err != nil {
		t.Fatal(err)
	}
	return path
}

// opened restores the snapshot at path and opens the log at logPath with
// it, as serve does, and requires the state equal a replay. It returns the
// state and what was logged.
func opened(t *testing.T, logPath, path, build string) (*State, string) {
	t.Helper()
	var logged bytes.Buffer
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	state := Restore(path, build, withIdeas, ideas.Streams())
	s, err := store.Open(logPath, withIdeas, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := Equal(s, state); err != nil {
		t.Errorf("%s", err)
	}
	return state, logged.String()
}

// copyOf copies the log at path into a new folder, with SQL run on the
// copy's rows, and returns the copy's path.
func copyOf(t *testing.T, path string, statements ...string) string {
	t.Helper()
	copied := filepath.Join(t.TempDir(), "agentry.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`VACUUM INTO ?`, copied); err != nil {
		t.Fatal(err)
	}
	changed, err := sql.Open("sqlite", copied)
	if err != nil {
		t.Fatal(err)
	}
	defer changed.Close()
	for _, statement := range statements {
		if _, err := changed.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return copied
}

// Equivalence test 3: every way a snapshot can't be used ends in a whole
// replay, equal to the log, and logs why.
func TestEveryFallbackReplaysTheWholeLogAndSaysWhy(t *testing.T) {
	const cut = 12
	proposals := fixtureLogs()["broken then more"]
	logPath, _ := seededLog(t, proposals...)
	shorterPath, _ := seededLog(t, proposals[:cut-2]...)
	// The task's worktree is gone from the snapshot, so the worktree.moved
	// after it panics in the tail, which a whole replay never meets.
	movedLater := append(slices.Clone(proposals[:cut]),
		proposal("task-1", worker, events.WorktreeReady, map[string]any{"path": "/w", "branch": "b", "port": float64(7901)}),
		proposal("task-1", worker, events.WorktreeMoved, map[string]any{"worker": "laptop", "path": "/l/w", "port": float64(7902)}))
	movedPath, _ := seededLog(t, movedLater...)

	cases := []struct {
		name    string
		logPath string // the log opened with the snapshot
		damage  func(t *testing.T, path string) string
		build   string
		want    string // in what is logged
		bad     bool   // the file is kept as .bad
	}{
		{"missing", logPath, func(t *testing.T, path string) string { os.Remove(path); return path }, "build-1", "there is no snapshot", false},
		{"truncated", logPath, func(t *testing.T, path string) string { return truncated(t, path) }, "build-1", "torn or corrupt", true},
		{"a body byte flipped", logPath, func(t *testing.T, path string) string { return flipped(t, path) }, "build-1", "sha256 differs", true},
		{"another build", logPath, nil, "build-2", "from other code", false},
		{"a folder", logPath, func(t *testing.T, path string) string { return folder(t, path) }, "build-1", "can't be read", false},
		{"not ours to read", logPath, func(t *testing.T, path string) string { return unreadable(t, path) }, "build-1", "can't be read", false},
		{"past the end", shorterPath, nil, "build-1", "past the log's last", false},
		{"no anchor row", copyOf(t, logPath, `DROP TRIGGER events_no_delete`, `DELETE FROM events WHERE position = 12`), nil, "build-1", "no event at its position", false},
		{"the anchor row changed", copyOf(t, logPath, `DROP TRIGGER events_no_update`, `UPDATE events SET data = '{"title":"Other"}' WHERE position = 12`), nil, "build-1", "not the one it was written at", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := snapshotted(t, copyOf(t, logPath), cut, nil)
			if c.damage != nil {
				path = c.damage(t, path)
			}
			state, logged := opened(t, c.logPath, path, c.build)
			if state.resumed != 0 {
				t.Errorf("went on from the snapshot at %d", state.resumed)
			}
			if !strings.Contains(logged, c.want) {
				t.Errorf("logged %q, want %q in it", logged, c.want)
			}
			if _, err := os.Stat(path + ".bad"); (err == nil) != c.bad {
				t.Errorf("kept as .bad: %v, want %v", err == nil, c.bad)
			}
		})
	}
	t.Run("the tail panics", func(t *testing.T) {
		path := snapshotted(t, movedPath, int64(len(movedLater)-1), func(s *State) {
			task := s.tasks["task-1"].Clone()
			task.Worktree = nil
			s.tasks["task-1"] = task
		})
		state, logged := opened(t, movedPath, path, "build-1")
		if state.resumed != 0 || !strings.Contains(logged, "an event after it broke a stream") {
			t.Errorf("went on from %d, logged %q", state.resumed, logged)
		}
		if state.Broken("task-1") != nil {
			t.Errorf("task-1 is broken after a whole replay: %v", state.Broken("task-1"))
		}
	})
}

// folder puts a folder where the snapshot was.
func folder(t *testing.T, path string) string {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// unreadable takes away the owner's leave to read the snapshot. Root reads
// it anyway, so the case is skipped there.
func unreadable(t *testing.T, path string) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 0000")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	return path
}

func truncated(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, info.Size()/2); err != nil {
		t.Fatal(err)
	}
	return path
}

// flipped flips one byte in the middle of the body.
func flipped(t *testing.T, path string) string {
	t.Helper()
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.IndexByte(written, '\n') + 1
	written[body+(len(written)-body)/2] ^= 0xff
	if err := os.WriteFile(path, written, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A corrupt snapshot is kept once: the next corrupt one replaces it.
func TestACorruptSnapshotIsKeptOnce(t *testing.T) {
	logPath, _ := seededLog(t, someProposals()...)
	var kept [][]byte
	for range 2 {
		path := flipped(t, snapshotted(t, logPath, 5, nil))
		written, _ := os.ReadFile(path)
		kept = append(kept, written)
		opened(t, logPath, path, "build-1")
	}
	found, _ := filepath.Glob(filepath.Join(filepath.Dir(logPath), "state.snapshot*"))
	if len(found) != 1 || filepath.Base(found[0]) != "state.snapshot.bad" {
		t.Fatalf("left %v, want only state.snapshot.bad", found)
	}
	if bad, _ := os.ReadFile(found[0]); !bytes.Equal(bad, kept[1]) {
		t.Error("state.snapshot.bad is not the last corrupt snapshot")
	}
}

// archivedLog is the every stream fixture with task-1 archived at the end,
// as the archiver leaves it: task-1's rows gone, and its marker, keeping
// the summary the list showed, alone in its stream.
func archivedLog(t *testing.T) string {
	t.Helper()
	path, _ := seededLog(t, someProposals()...)
	s, err := store.Open(path, withIdeas, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	own, err := s.Stream("task-1")
	if err != nil {
		t.Fatal(err)
	}
	roster, err := s.Stream(events.AgentsStream)
	if err != nil {
		t.Fatal(err)
	}
	propose(t, s, taskstest.Marker(own, tasks.SummaryOf(tasks.FromEvents(own), agents.FromEvents(roster))))
	s.Close()
	return copyOf(t, path, fmt.Sprintf(`DELETE FROM events WHERE stream = 'task-1' AND position <= %d`, own[len(own)-1].Position))
}

// A marker alone in its stream folds to its summary: the state equals a
// replay, lists the summary in task-1's place and counts task-1 for the
// next task, and a snapshot at every position carries the summary.
func TestAMarkerAloneFoldsToItsSummary(t *testing.T) {
	logPath := archivedLog(t)
	state := New(withIdeas, ideas.Streams())
	s, err := store.Open(logPath, withIdeas, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Equal(s, state); err != nil {
		t.Error(err)
	}
	if only, err := s.Stream("task-1"); err != nil || len(only) != 1 || only[0].Type != events.TaskArchived {
		t.Fatalf("task-1's stream should be its marker alone: %v, %v", only, err)
	}
	summaries := state.Summaries()
	if len(summaries) != 2 || summaries[0].ID != "task-1" || summaries[0].Title != "Speed up the list" || !slices.Equal(summaries[0].Labels, []string{"web"}) {
		t.Errorf("the summaries are %+v, want task-1's archived one first", summaries)
	}
	if next := state.NextTask(); next != "task-4" {
		t.Errorf("the next task is %s, want task-4", next)
	}
	all, err := everyEvent(s)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	var cuts []int64
	for _, e := range all {
		cuts = append(cuts, e.Position)
	}
	if err := EqualThroughSnapshots(logPath, withIdeas, t.TempDir(), cuts, ideas.Streams()); err != nil {
		t.Error(err)
	}
	restored := Restore(snapshotted(t, logPath, cuts[len(cuts)-1], nil), "build-1", withIdeas, ideas.Streams())
	if restored.snapshot == nil {
		t.Fatal("the snapshot wasn't restored")
	}
	if !reflect.DeepEqual(restored.archived, state.archived) {
		t.Errorf("restored summaries %+v, want %+v", restored.archived, state.archived)
	}
}

// A task archived and unarchived with its rows in the log, and changed
// after: a snapshot at every position equals a replay, so a snapshot that
// holds the summary takes the unarchiving in its tail as a refold.
func TestASnapshotTakesAnUnarchivingInItsTail(t *testing.T) {
	path, _ := seededLog(t, someProposals()...)
	s, err := store.Open(path, withIdeas, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	own, err := s.Stream("task-1")
	if err != nil {
		t.Fatal(err)
	}
	marker, _, err := s.Propose(taskstest.Marker(own, tasks.SummaryOf(tasks.FromEvents(own), nil)))
	if err != nil {
		t.Fatal(err)
	}
	propose(t, s, taskstest.Unarchived("task-1", marker.Position))
	propose(t, s, proposal("task-1", you, events.TaskRenamed, map[string]any{"title": "Back"}))
	last := s.LastPosition()
	s.Close()
	var cuts []int64
	for cut := marker.Position - 1; cut <= last; cut++ {
		cuts = append(cuts, cut)
	}
	if err := EqualThroughSnapshots(path, withIdeas, t.TempDir(), cuts, ideas.Streams()); err != nil {
		t.Error(err)
	}
}

// beforeSummaries is the shape of a snapshot's first chunk before the state
// kept summaries, as far as the archived set goes: a name and a flag.
type beforeSummaries struct {
	Position int64
	Archived map[string]bool
}

// A snapshot from before the state kept summaries is never read as one
// with them. Older code is another build, so its snapshot is of no use
// (the version tag); and even a body of the old shape under this build's
// tag fails to decode instead of being misread. Either way the whole log
// replays, the archived task's summary included.
func TestASnapshotFromBeforeSummariesReplays(t *testing.T) {
	logPath := archivedLog(t)
	log, err := store.Open(logPath, withIdeas, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	last := log.LastPosition()
	anchor, _, err := log.Hash(last)
	log.Close()
	if err != nil {
		t.Fatal(err)
	}
	oldShape := func(t *testing.T, path string) string {
		var body bytes.Buffer
		if err := writeChunk(&body, beforeSummaries{Position: last, Archived: map[string]bool{"task-1": true}}); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body.Bytes())
		line, err := json.Marshal(header{Format: snapshotFormat, Build: "build-1", Position: last, Anchor: anchor,
			Size: int64(body.Len()), SHA256: hex.EncodeToString(sum[:])})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(append(line, '\n'), body.Bytes()...), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, c := range []struct {
		name   string
		damage func(t *testing.T, path string) string
		build  string
		want   string
	}{
		{"older code", nil, "build-2", "from other code"},
		{"the old shape under this build", oldShape, "build-1", "torn or corrupt"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := snapshotted(t, logPath, last, nil)
			if c.damage != nil {
				path = c.damage(t, path)
			}
			state, logged := opened(t, logPath, path, c.build)
			if state.resumed != 0 || !strings.Contains(logged, c.want) {
				t.Errorf("went on from %d, logged %q, want %q in it", state.resumed, logged, c.want)
			}
			if summary, archived := state.archived["task-1"]; !archived || summary.Title != "Speed up the list" {
				t.Errorf("after the replay task-1 is %+v, archived %v", summary, archived)
			}
		})
	}
}

func TestASnapshotIsReadableByItsOwnerAlone(t *testing.T) {
	logPath, _ := seededLog(t, someProposals()...)
	info, err := os.Stat(snapshotted(t, logPath, 5, nil))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode %v, want 0600", mode)
	}
}

// A write that fails leaves the old file as it was, and no temporary file.
func TestAFailedWriteLeavesTheOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.snapshot")
	if err := writeAtomically(path, func(w io.Writer) error { _, err := io.WriteString(w, "old"); return err }); err != nil {
		t.Fatal(err)
	}
	failed := writeAtomically(path, func(w io.Writer) error {
		io.WriteString(w, "half of the new")
		return io.ErrShortWrite
	})
	if failed == nil {
		t.Fatal("the failed write returned no error")
	}
	if written, _ := os.ReadFile(path); string(written) != "old" {
		t.Errorf("the file holds %q after a failed write", written)
	}
	if found, _ := filepath.Glob(leftovers(path)); len(found) > 0 {
		t.Errorf("left %v behind", found)
	}
}

func TestRemoveLeftoversRemovesOnlyUnfinishedWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.snapshot")
	for _, name := range []string{"state.snapshot", "state.snapshot.bad", "state.snapshot.tmp-123", "state.snapshot.tmp-abc",
		"state.snapshot.tmp", "other.snapshot.tmp-1", "agentry.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveLeftovers(path); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, entry := range entries {
		left = append(left, entry.Name())
	}
	if want := []string{"agentry.db", "other.snapshot.tmp-1", "state.snapshot", "state.snapshot.bad", "state.snapshot.tmp"}; !slices.Equal(left, want) {
		t.Errorf("left %v, want %v", left, want)
	}
}
