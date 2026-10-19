package current_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/ideas"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/storetest"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
)

// What the snapshot targets of section 6 allow.
const (
	tailEvents       = 10_000 // Target 3: the most a snapshot is behind
	tailLimit        = 100 * time.Millisecond
	startupLimit     = 500 * time.Millisecond // Target 1, with GOMAXPROCS 2 or more
	startupLimitOne  = 750 * time.Millisecond // Target 1, on one core
	snapshotCPULimit = 500 * time.Millisecond // Target 6
)

const benchBuild = "bench"

// benchSnapshots measures Targets 3, 1 and 6 on the log at path, whose
// whole state is state, and logs each against its limit:
//
//   - 3: Open from a snapshot tail events behind the log, against one at
//     its last event; the difference is the tail's apply.
//   - 1: Open from a snapshot of the working set, state with the
//     archivable tasks archived, as the archiver will leave it, on 1 and 2
//     cores.
//   - 6: writing that snapshot on one core: wall time at GOMAXPROCS 1,
//     which is at least the CPU time it takes.
//
// It archives the archivable tasks out of state. Opens and writes are min
// of 3.
func benchSnapshots(t *testing.T, path string, state *current.State, archivable []*tasks.Task, tail int64) {
	dir := t.TempDir()
	log, err := store.Open(path, storetest.WithIdeas(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	last := log.LastPosition()

	whole, behind := filepath.Join(dir, "whole.snapshot"), filepath.Join(dir, "behind.snapshot")
	writeSnapshot(t, whole, state, log)
	writeSnapshot(t, behind, stateAt(t, path, last-tail), log) // the log's event at the cut is the anchor
	noTail := minOf3On(2, func() { resumed(t, path, whole, last) })
	withTail := minOf3On(2, func() { resumed(t, path, behind, last-tail) })
	t.Logf("  3 a tail of %d events <= %s: %s (Open from %d behind: %s, from the last event: %s)",
		tail, seconds(tailLimit), seconds(withTail-noTail), tail, seconds(withTail), seconds(noTail))

	for _, task := range archivable {
		state.ArchiveInPlace(tasks.SummaryOf(task, state.Roster()))
	}
	working := filepath.Join(dir, "working.snapshot")
	wrote := minOf3On(1, func() { writeSnapshot(t, working, state, log) })
	info, err := os.Stat(working)
	if err != nil {
		t.Fatal(err)
	}
	for _, cores := range []int{1, 2} {
		limit := startupLimit
		if cores == 1 {
			limit = startupLimitOne
		}
		took := minOf3On(cores, func() { resumed(t, path, working, last) })
		t.Logf("  1 startup from a snapshot of the working set on %d core(s) <= %s: %s (%.1f MB, %d tasks archived)",
			cores, seconds(limit), seconds(took), float64(info.Size())/1e6, len(archivable))
	}
	t.Logf("  6 a snapshot write <= %s of CPU: %s on one core", seconds(snapshotCPULimit), seconds(wrote))
}

func writeSnapshot(t *testing.T, path string, state *current.State, log *store.Store) {
	t.Helper()
	if err := current.WriteSnapshot(path, state, log, benchBuild); err != nil {
		t.Fatal(err)
	}
}

// resumed restores the snapshot at path and opens the log at logPath with
// it, as serve does, and fails unless Open went on from position.
func resumed(t *testing.T, logPath, path string, position int64) {
	t.Helper()
	state := current.Restore(path, benchBuild, storetest.WithIdeas(), ideas.Streams())
	s, err := store.Open(logPath, storetest.WithIdeas(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if state.Resumed() != position {
		t.Fatalf("Open went on from %d, not the snapshot at %d", state.Resumed(), position)
	}
}

// stateAt is the state of the log at path up to cut, as a whole replay of
// that much builds it: the replay of a copy with the later events deleted.
func stateAt(t *testing.T, path string, cut int64) *current.State {
	t.Helper()
	copied := filepath.Join(t.TempDir(), "cut.log")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`VACUUM INTO ?`, copied); err != nil {
		t.Fatal(err)
	}
	cutDB, err := sql.Open("sqlite", copied)
	if err != nil {
		t.Fatal(err)
	}
	defer cutDB.Close()
	for _, statement := range []string{`DROP TRIGGER events_no_delete`, `DELETE FROM events WHERE position > ?`} {
		if _, err := cutDB.Exec(statement, cut); err != nil {
			t.Fatal(err)
		}
	}
	state := current.New(storetest.WithIdeas(), ideas.Streams())
	s, err := store.Open(copied, storetest.WithIdeas(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	return state
}

// The snapshot benchmark runs, on a small log made here, so a change that
// breaks it shows without AGENTRY_BENCH_DB.
func TestTheSnapshotBenchRunsOnASmallLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.log")
	state := current.New(storetest.WithIdeas(), ideas.Streams())
	s, err := store.Open(path, storetest.WithIdeas(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	you := events.Actor{Kind: events.Human, ID: "you"}
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		proposeOrFail(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskCreated,
			Data: map[string]any{"title": id, "repo": "/r", "coordinator": "tech-lead"}})
		for i := range 10 {
			proposeOrFail(t, s, events.Proposal{Stream: id, Actor: you, Type: events.TaskRenamed,
				Data: map[string]any{"title": id + " " + string(rune('a'+i))}})
		}
	}
	s.Close()
	benchSnapshots(t, path, state, state.Tasks()[:1], 5)
}

func proposeOrFail(t *testing.T, s *store.Store, p events.Proposal) {
	t.Helper()
	if _, rejection, err := s.Propose(p); err != nil || rejection != nil {
		t.Fatalf("%s on %s: %v %v", p.Type, p.Stream, err, rejection)
	}
}
