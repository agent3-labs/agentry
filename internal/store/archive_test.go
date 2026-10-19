package store

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gitlab.com/dev.chempa/agentry/internal/events"
)

// No archiver exists yet (build step 7), so these tests write the markers
// and run the deletes themselves, as Store.Archive and Store.Unarchive
// will: the marker and the delete, or the rows and the unarchiving, in one
// transaction.

var archiver = events.Actor{Kind: events.System, ID: "archiver"}

const blob = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// marker is a task.archived covering stream's rows from created to through.
func marker(stream string, created, through int64, count int) events.Proposal {
	return events.Proposal{Stream: stream, Actor: archiver, Type: events.TaskArchived,
		Data: map[string]any{"through": through, "count": count, "archive": blob, "created": created, "summary": map[string]any{"title": "Remove the TODOs"}}}
}

func unarchived(stream string, marker int64) events.Proposal {
	return events.Proposal{Stream: stream, Actor: archiver, Type: events.TaskUnarchived,
		Data: map[string]any{"marker": marker, "archive": blob}}
}

// inTransaction runs change in one transaction and commits it.
func inTransaction(t *testing.T, s *Store, change func(tx *sql.Tx)) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	change(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func appendIn(t *testing.T, tx *sql.Tx, p events.Proposal) events.Event {
	t.Helper()
	e, err := appendEvent(tx, p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// deleteIn deletes stream's rows from position from to through and says
// how many went, or the error.
func deleteIn(tx *sql.Tx, stream string, from, through int64) (int64, error) {
	result, err := tx.Exec(`DELETE FROM events WHERE stream = ? AND position BETWEEN ? AND ?`, stream, from, through)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func refusedAsAppendOnly(err error) bool {
	return err != nil && strings.Contains(err.Error(), "events are append-only")
}

// acceptAll proposes each, and fails the test on any refusal.
func acceptAll(t *testing.T, s *Store, proposals ...events.Proposal) {
	t.Helper()
	for _, p := range proposals {
		if _, rejection, err := s.Propose(p); rejection != nil || err != nil {
			t.Fatal(p.Type, rejection, err)
		}
	}
}

func renamed(stream string) events.Proposal {
	return events.Proposal{Stream: stream, Actor: you, Type: events.TaskRenamed, Data: map[string]any{"title": "Remove every TODO"}}
}

func TestAStrayDeleteIsRefused(t *testing.T) {
	s := openTestStore(t)
	acceptAll(t, s, newTask("task-1"), renamed("task-1"))
	for _, statement := range []string{`DELETE FROM events`, `DELETE FROM events WHERE position = 1`} {
		if _, err := s.db.Exec(statement); !refusedAsAppendOnly(err) {
			t.Errorf("%s: %v, want refused", statement, err)
		}
	}
}

// The marker allows deleting exactly the rows it covers, in its own
// transaction before it commits: none past "through". (None of another
// stream either: TestAMarkerAllowsNoOtherStreamsRows.)
func TestAMarkerAllowsDeletingOnlyTheRowsItCovers(t *testing.T) {
	s := openTestStore(t)
	acceptAll(t, s, newTask("task-1"), renamed("task-1"), newTask("task-2"), renamed("task-1"))
	// task-1 is at 1, 2 and 4; the marker covers 1 and 2.
	inTransaction(t, s, func(tx *sql.Tx) {
		if _, err := deleteIn(tx, "task-1", 1, 2); !refusedAsAppendOnly(err) {
			t.Errorf("before the marker: %v, want refused", err)
		}
		appendIn(t, tx, marker("task-1", 1, 2, 2))
		if _, err := tx.Exec(`UPDATE events SET data = '{}' WHERE position = 1`); !refusedAsAppendOnly(err) {
			t.Errorf("updating a row the marker covers: %v, want refused", err)
		}
		if deleted, err := deleteIn(tx, "task-1", 1, 2); err != nil || deleted != 2 {
			t.Errorf("the rows the marker covers: %d deleted, %v", deleted, err)
		}
		if _, err := deleteIn(tx, "task-1", 4, 4); !refusedAsAppendOnly(err) {
			t.Errorf("a row past through: %v, want refused", err)
		}
	})
	var left []int64
	rows, err := s.db.Query(`SELECT position FROM events ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var position int64
		rows.Scan(&position)
		left = append(left, position)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []int64{3, 4, 5}; !slices.Equal(left, want) {
		t.Errorf("rows left %v, want %v (task-2, task-1's last, the marker)", left, want)
	}
}

// A marker allows only its own stream's rows: another task's row that
// sits between the marked task's rows, inside "through", stays.
func TestAMarkerAllowsNoOtherStreamsRows(t *testing.T) {
	s := openTestStore(t)
	acceptAll(t, s, newTask("task-1"), newTask("task-2"), renamed("task-1"))
	// task-1 is at 1 and 3, task-2 at 2; the marker covers 1 to 3.
	inTransaction(t, s, func(tx *sql.Tx) {
		appendIn(t, tx, marker("task-1", 1, 3, 2))
		if _, err := deleteIn(tx, "task-2", 2, 2); !refusedAsAppendOnly(err) {
			t.Errorf("task-2's row inside task-1's marker: %v, want refused", err)
		}
	})
}

// Once task.unarchived follows the marker, its rows are the task's again:
// the delete the marker allowed is refused. A second marker after that
// allows it again.
func TestUnarchivingEndsWhatTheMarkerAllows(t *testing.T) {
	s := openTestStore(t)
	acceptAll(t, s, newTask("task-1"), renamed("task-1"))
	var first events.Event
	inTransaction(t, s, func(tx *sql.Tx) {
		first = appendIn(t, tx, marker("task-1", 1, 2, 2))
		appendIn(t, tx, unarchived("task-1", first.Position))
	})
	inTransaction(t, s, func(tx *sql.Tx) {
		if _, err := deleteIn(tx, "task-1", 1, 2); !refusedAsAppendOnly(err) {
			t.Errorf("after task.unarchived: %v, want refused", err)
		}
	})
	inTransaction(t, s, func(tx *sql.Tx) {
		second := appendIn(t, tx, marker("task-1", 1, first.Position+1, 4))
		if deleted, err := deleteIn(tx, "task-1", 1, second.Position-1); err != nil || deleted != 4 {
			t.Errorf("archived again: %d deleted, %v, want the 4 rows before the second marker", deleted, err)
		}
	})
}

// versionZero is the schema every build before the migrations made: the
// ungated delete trigger, no user_version.
const versionZero = `
CREATE TABLE events (
	position    INTEGER PRIMARY KEY AUTOINCREMENT,
	at          TEXT NOT NULL,
	stream      TEXT NOT NULL,
	actor_kind  TEXT NOT NULL,
	actor_id    TEXT NOT NULL,
	type        TEXT NOT NULL,
	correlation TEXT NOT NULL DEFAULT '',
	data        TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX events_by_stream ON events (stream, position);
CREATE TRIGGER events_no_update BEFORE UPDATE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
INSERT INTO events (at, stream, actor_kind, actor_id, type, data) VALUES
	('2026-10-19T09:00:00Z', 'task-1', 'human', 'you', 'task.created', '{"title":"Remove the TODOs","repo":"/src/app","coordinator":"tech-lead"}'),
	('2026-10-19T09:00:01Z', 'task-1', 'human', 'you', 'task.renamed', '{"title":"Remove every TODO"}'),
	('2026-10-19T09:00:02Z', 'task-2', 'human', 'you', 'task.created', '{"title":"Fix the list","repo":"/src/app","coordinator":"tech-lead"}');
`

// schemaOf reads what a test checks of a file's schema with a connection
// of its own: user_version, auto_vacuum and the delete trigger's SQL.
func schemaOf(t *testing.T, path string) (version, autoVacuum int, trigger string) {
	t.Helper()
	db, err := sql.Open(Driver, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'events_no_delete'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	return version, autoVacuum, trigger
}

func execOn(t *testing.T, path, statements string) {
	t.Helper()
	db, err := sql.Open(Driver, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(statements); err != nil {
		t.Fatal(err)
	}
}

// A log made before the migrations opens at version 1, with the gated
// trigger and every event as it was; opening it again runs nothing.
func TestAVersionZeroLogMigratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentry.db")
	execOn(t, path, versionZero)

	s, model := openAt(t, path)
	log, err := s.After(0, 10)
	if err != nil || len(log) != 3 || log[1].Text("title") != "Remove every TODO" || !model.has("task-2") {
		t.Errorf("the events after migrating: %v %v", log, err)
	}
	if _, err := s.db.Exec(`DELETE FROM events WHERE position = 1`); !refusedAsAppendOnly(err) {
		t.Errorf("a stray delete after migrating: %v, want refused", err)
	}
	s.Close()
	version, _, trigger := schemaOf(t, path)
	if version != 1 || !strings.Contains(trigger, "task.archived") {
		t.Fatalf("version %d, trigger %s; want 1 and the gated trigger", version, trigger)
	}

	// Had the migration run again, it would have put back the gated trigger.
	sentinel := `CREATE TRIGGER events_no_delete BEFORE DELETE ON events
	BEGIN SELECT RAISE(ABORT, 'events are append-only: sentinel'); END`
	execOn(t, path, `DROP TRIGGER events_no_delete; `+sentinel)
	again, _ := openAt(t, path)
	again.Close()
	if version, _, trigger := schemaOf(t, path); version != 1 || trigger != sentinel {
		t.Errorf("after a second open: version %d, trigger %s; the migration ran again", version, trigger)
	}
}

func TestANewLogStartsAtTheLatestSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentry.db")
	s, _ := openAt(t, path)
	s.Close()
	version, autoVacuum, trigger := schemaOf(t, path)
	if version != len(migrations) || autoVacuum != 2 || !strings.Contains(trigger, "task.archived") {
		t.Errorf("version %d, auto_vacuum %d, trigger %s; want %d, 2 (incremental) and the gated trigger", version, autoVacuum, trigger, len(migrations))
	}
}

// A log from a newer build isn't opened: its schema may refuse what this
// build would write, or allow what it shouldn't.
func TestALogFromANewerBuildIsNotOpened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentry.db")
	s, _ := openAt(t, path)
	s.Close()
	execOn(t, path, `PRAGMA user_version = 99`)
	if s, err := Open(path, withNotes, nil, nil); err == nil || !strings.Contains(err.Error(), "schema version 99") {
		if err == nil {
			s.Close()
		}
		t.Errorf("opening a version 99 log: %v", err)
	}
}

// A task counts once for the next id however often it was archived: the
// next task never takes an archived task's id.
func TestCreateCountsArchivedTasksOnce(t *testing.T) {
	s := openTestStore(t)
	create := func(want string) {
		t.Helper()
		made, rejection, err := s.Create(events.TaskPrefix, newTask(""))
		if err != nil || rejection != nil || made.Stream != want {
			t.Fatalf("Create: %s %v %v, want %s", made.Stream, rejection, err, want)
		}
	}
	create("task-1")
	create("task-2")
	acceptAll(t, s, renamed("task-1")) // task-1 at 1 and 3
	archive := func() {
		t.Helper()
		inTransaction(t, s, func(tx *sql.Tx) {
			var through int64
			var count int
			if err := tx.QueryRow(`SELECT MAX(position), COUNT(*) FROM events WHERE stream = 'task-1'`).Scan(&through, &count); err != nil {
				t.Fatal(err)
			}
			appendIn(t, tx, marker("task-1", 1, through, count))
			if deleted, err := deleteIn(tx, "task-1", 1, through); err != nil || deleted != int64(count) {
				t.Fatalf("archiving task-1: %d deleted, %v, want %d", deleted, err, count)
			}
		})
	}

	archive()
	create("task-3")

	// The rows back with their own positions, as Store.Unarchive will put
	// them: task.created at 1 again.
	inTransaction(t, s, func(tx *sql.Tx) {
		var at int64
		if err := tx.QueryRow(`SELECT position FROM events WHERE type = 'task.archived'`).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO events (position, at, stream, actor_kind, actor_id, type, data) VALUES
			(1, '2026-10-19T09:00:00Z', 'task-1', 'human', 'you', 'task.created', '{"title":"Remove the TODOs","repo":"/src/app","coordinator":"tech-lead"}'),
			(3, '2026-10-19T09:00:01Z', 'task-1', 'human', 'you', 'task.renamed', '{"title":"Remove every TODO"}')`); err != nil {
			t.Fatal(err)
		}
		appendIn(t, tx, unarchived("task-1", at))
	})
	create("task-4")

	archive() // its second marker names the same task.created
	create("task-5")
}
