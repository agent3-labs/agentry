package current_test

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/ideas"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/storetest"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
)

// TestStartupBench measures startup on a synthetic log grown from a copy of
// a real agentry.db, against the Targets of docs/design/log-growth.md,
// section 6. It is skipped unless AGENTRY_BENCH_DB names the copy, which it
// only reads. AGENTRY_BENCH_GB is the history to grow to, 1 by default, and
// AGENTRY_BENCH_WORKING=2 doubles the working set W:
//
//	AGENTRY_BENCH_DB=<copy>/agentry.db go test ./internal/current -run TestStartupBench -v -count=1 -timeout 30m
//
// Targets 1, 3 and 6 are measured with snapshots (build step 4); 2, 4 and 5
// are still case (a), the read model alone with nothing archived, and are
// not expected to pass until the archiver (step 7).
func TestStartupBench(t *testing.T) {
	source := os.Getenv("AGENTRY_BENCH_DB")
	if source == "" {
		t.Skip("AGENTRY_BENCH_DB is not set")
	}
	gb := benchSetting(t, "AGENTRY_BENCH_GB", 1)
	working := int(benchSetting(t, "AGENTRY_BENCH_WORKING", 1))
	path := filepath.Join(t.TempDir(), "agentry.db")
	start := time.Now()
	grown := storetest.Grow(t, source, path, int64(gb*1e9), working)
	t.Logf("grew %.2f rounds of the copy in %s: %d tasks, %d open, the last event at %s",
		grown.Rounds, time.Since(start).Round(time.Second), grown.Tasks, grown.Open, grown.End.Format(time.DateOnly))
	mix := storetest.MixOf(t, path)
	checkMix(t, storetest.MixOf(t, source), grown.Round, mix)

	startup, state, held := benchStartup(t, path)
	w := workingSetOf(t, path, state, grown.End)
	h := float64(mix.File) / 1e9
	t.Logf("working set: %d open tasks, %d done in the last %d days, %d events in the state once the other %d done tasks are archived",
		w.open, len(w.kept)-w.open, storetest.WorkingDays, w.events, len(w.archivable))

	t.Logf("Targets (GOMAXPROCS %d):", runtime.GOMAXPROCS(0))
	if limit := fullReplayLimit(gb); limit > 0 {
		t.Logf("  2 full replay <= %s: %s", seconds(limit), seconds(startup))
	}
	fileLimit := float64(w.open)*1e6 + float64(storetest.WorkingDays*working)*6e6 + 35e6*h
	t.Logf("  4 live file <= %.0f MB (1 MB x %d open + 6 MB x %d busy days + 35 MB x %.2f GB): %.0f MB",
		fileLimit/1e6, w.open, storetest.WorkingDays*working, h, float64(mix.File)/1e6)
	heapLimit := 450*float64(w.events) + 1e3*float64(len(w.archivable))
	t.Logf("  5 heap <= %.0f MB (450 B x %d events + 1 KB x %d archived): %.0f MB",
		heapLimit/1e6, w.events, len(w.archivable), float64(held)/1e6)

	benchBodyDecode(t, state, w, working)
	benchSummaries(t, state.Roster(), w.archivable)
	benchSnapshots(t, path, state, w.archivable, tailEvents)
}

func benchSetting(t *testing.T, name string, otherwise float64) float64 {
	value := os.Getenv(name)
	if value == "" {
		return otherwise
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n <= 0 {
		t.Fatalf("%s=%q: want a number above 0", name, value)
	}
	return n
}

// fullReplayLimit is Target 2 at gb of history, 0 where it sets none.
func fullReplayLimit(gb float64) time.Duration {
	switch {
	case gb <= 1:
		return 2500 * time.Millisecond
	case gb <= 10:
		return 5 * time.Second
	}
	return 0
}

func seconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 2, 64) + " s" }

// checkMix fails unless the synthetic log has the copy's events per GB of
// file and one round's share of the data bytes for each event type, within
// 5%. Types under 1% of a round's bytes are counted together, as "the
// rest".
func checkMix(t *testing.T, copied, round, synthetic storetest.Mix) {
	within := func(what, against string, want, got float64) {
		t.Logf("  %-28s %s %12.4g, synthetic %12.4g (%+.1f%%)", what, against, want, got, 100*(got/want-1))
		if math.Abs(got/want-1) > 0.05 {
			t.Errorf("%s differs by more than 5%%", what)
		}
	}
	t.Log("mix, the synthetic log against the copy and one round of it:")
	perGB := func(m storetest.Mix) float64 { return float64(m.Events) / (float64(m.File) / 1e9) }
	within("events per GB", "copy ", perGB(copied), perGB(synthetic))
	share := func(m storetest.Mix, typ events.Type) float64 {
		var all int64
		for _, c := range m.Types {
			all += c.Bytes
		}
		return float64(m.Types[typ].Bytes) / float64(all)
	}
	var big []events.Type
	rest := [2]float64{1, 1}
	for typ := range round.Types {
		if share(round, typ) >= 0.01 {
			big = append(big, typ)
		}
	}
	slices.Sort(big)
	for _, typ := range big {
		within(string(typ)+" bytes", "round", share(round, typ), share(synthetic, typ))
		rest[0] -= share(round, typ)
		rest[1] -= share(synthetic, typ)
	}
	within("the rest's bytes", "round", rest[0], rest[1])
}

// benchStartup times store.Open on a fresh state, which loads the whole log
// and ends with Loaded, min of 3. It returns the state of one more open and
// the heap it holds once the store is closed and its events dropped.
func benchStartup(t *testing.T, path string) (time.Duration, *current.State, uint64) {
	open := func() *current.State {
		state := current.New(storetest.WithIdeas(), ideas.Streams())
		s, err := store.Open(path, storetest.WithIdeas(), state, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return state
	}
	var fastest time.Duration
	for range 3 {
		runtime.GC()
		start := time.Now()
		open()
		if took := time.Since(start); fastest == 0 || took < fastest {
			fastest = took
		}
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	state := open()
	runtime.GC()
	runtime.ReadMemStats(&after)
	for _, id := range state.TaskIDs() {
		if err := state.Broken(id); err != nil {
			t.Errorf("%s is broken: %v", id, err)
		}
	}
	for _, stream := range []string{events.AgentsStream, events.WorkersStream, events.ConnectionsStream} {
		if err := state.Broken(stream); err != nil {
			t.Errorf("%s is broken: %v", stream, err)
		}
	}
	return fastest, state, after.HeapAlloc - before.HeapAlloc
}

// workingSet is what the state would keep whole once the archiver had run:
// the open tasks and those done in the last WorkingDays.
type workingSet struct {
	kept       []*tasks.Task // in the order the tasks were made
	open       int           // of kept
	archivable []*tasks.Task // done before that
	events     int           // accepted events left in the state: every stream's but the archivable tasks'
}

func workingSetOf(t *testing.T, path string, state *current.State, end time.Time) workingSet {
	counts := acceptedByStream(t, path)
	var w workingSet
	for _, c := range counts {
		w.events += c
	}
	since := end.Add(-storetest.WorkingDays * 24 * time.Hour)
	for _, task := range state.Tasks() {
		switch {
		case !task.IsDone:
			w.kept = append(w.kept, task)
			w.open++
		case task.DoneAt.After(since):
			w.kept = append(w.kept, task)
		default:
			w.archivable = append(w.archivable, task)
			w.events -= counts[task.ID]
		}
	}
	return w
}

func acceptedByStream(t *testing.T, path string) map[string]int {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT stream, COUNT(*) FROM events WHERE type != ? GROUP BY stream`, events.ProposalRejected)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var stream string
		var n int
		if err := rows.Scan(&stream, &n); err != nil {
			t.Fatal(err)
		}
		counts[stream] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return counts
}

// entry is one entry of a snapshot's body, as section 2 of the design has
// it: a task's saved form, an archived task's summary, or the roster. The
// real body is step 4's; this one is only for timing its decode.
type entry struct {
	Task    *tasks.Saved   `json:"task,omitempty"`
	Summary *tasks.Summary `json:"summary,omitempty"`
	Roster  agents.Saved   `json:"roster,omitempty"`
}

// The body sized for 100 GB of history (section 6, formula (e)): the
// working set's tasks, 292,000 events at 440 bytes of JSON, and 90,000
// archived summaries. Section 2's rule for leaving JSON: its decode on 2
// cores must fit in 0.5 s of startup less the fixed 0.16 s.
const (
	workingSetBody    = 128.5e6
	archivedSummaries = 90_000
	decodeBudget      = 340 * time.Millisecond
	gobStreams        = 64 // a gob body is this many streams, each with its own types, so goroutines can share them out
)

// benchBodyDecode times decoding a snapshot body sized for 100 GB of
// history on 1, 2 and 8 cores, as JSON with one entry to a line and as gob,
// and applies section 2's rule to the JSON time on 2. The tasks are the
// working set's, as many as fill the projected size, round and round; the
// summaries are the archivable tasks', the same way. The gob body has no
// presence flags for nil against empty, which a real one would need: they
// cost little beside the texts.
func benchBodyDecode(t *testing.T, state *current.State, w workingSet, working int) {
	if len(w.kept) == 0 || len(w.archivable) == 0 {
		t.Fatal("the synthetic log has no working set or nothing to archive: grow it larger")
	}
	roster := state.Roster()
	var entries []entry
	var lines bytes.Buffer
	add := func(e entry) int {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
		lines.Write(line)
		lines.WriteByte('\n')
		return len(line) + 1
	}
	add(entry{Roster: roster.Save()})
	saved := make([]tasks.Saved, len(w.kept))
	for i, task := range w.kept {
		saved[i] = task.Save()
	}
	for i, inTasks := 0, 0; float64(inTasks) < workingSetBody*float64(working); i++ {
		inTasks += add(entry{Task: &saved[i%len(saved)]})
	}
	for i := range archivedSummaries {
		summary := tasks.SummaryOf(w.archivable[i%len(w.archivable)], roster)
		add(entry{Summary: &summary})
	}
	streams := gobBody(t, entries)
	gobSize := 0
	for _, s := range streams {
		gobSize += len(s)
	}
	t.Logf("snapshot body for 100 GB: %d entries, JSON %.1f MB, gob %.1f MB (from the working set's %d tasks; projected %.1f MB)",
		len(entries), float64(lines.Len())/1e6, float64(gobSize)/1e6, len(w.kept), workingSetBody*float64(working)/1e6)

	jsonOnTwo, gobOnTwo := time.Duration(0), time.Duration(0)
	for _, cores := range []int{1, 2, 8} {
		jsonTook := minOf3On(cores, func() { decodeLines(t, lines.Bytes(), cores) })
		gobTook := minOf3On(cores, func() { decodeGob(t, streams, cores) })
		t.Logf("  decoded on %d goroutines (GOMAXPROCS %d), min of 3: JSON %s, gob %s", cores, cores, seconds(jsonTook), seconds(gobTook))
		if cores == 2 {
			jsonOnTwo, gobOnTwo = jsonTook, gobTook
		}
	}
	if jsonOnTwo <= decodeBudget {
		t.Logf("  rule: JSON takes %s on 2 goroutines, within %s, so the snapshot stays JSON", seconds(jsonOnTwo), seconds(decodeBudget))
	} else {
		t.Logf("  rule: JSON takes %s on 2 goroutines, over %s, so the snapshot is gob, which takes %s (within the budget: %v)",
			seconds(jsonOnTwo), seconds(decodeBudget), seconds(gobOnTwo), gobOnTwo <= decodeBudget)
	}
}

// minOf3On is the fastest of three runs of decode on cores cores, each
// after a collection, so one run's garbage isn't the next one's to collect.
func minOf3On(cores int, decode func()) time.Duration {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(cores))
	var fastest time.Duration
	for range 3 {
		runtime.GC()
		start := time.Now()
		decode()
		if took := time.Since(start); fastest == 0 || took < fastest {
			fastest = took
		}
	}
	return fastest
}

// decodeLines decodes every line of body on goroutines goroutines, each
// taking every goroutines-th line, and keeps what each decodes, as a load
// would: a task restored from its saved form, a summary, the roster. It
// holds them all until every line is done, so the timing pays for keeping
// them as a load does, not for garbage dropped as it goes.
func decodeLines(t *testing.T, body []byte, goroutines int) {
	lines := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n"))
	decoded := make([]any, len(lines))
	shareOut(t, len(lines), goroutines, func(i int) error {
		var e entry
		if err := json.Unmarshal(lines[i], &e); err != nil {
			return err
		}
		decoded[i] = e.kept()
		return nil
	})
}

// gobBody is the entries as gobStreams gob streams, each of a run of them.
func gobBody(t *testing.T, entries []entry) [][]byte {
	streams := make([][]byte, gobStreams)
	per := (len(entries) + gobStreams - 1) / gobStreams
	for i := range streams {
		var stream bytes.Buffer
		encoder := gob.NewEncoder(&stream)
		for _, e := range entries[min(i*per, len(entries)):min((i+1)*per, len(entries))] {
			if err := encoder.Encode(e); err != nil {
				t.Fatal(err)
			}
		}
		streams[i] = stream.Bytes()
	}
	return streams
}

// decodeGob decodes every stream on goroutines goroutines, as decodeLines
// decodes lines, holding what it decodes for the same reason.
func decodeGob(t *testing.T, streams [][]byte, goroutines int) {
	decoded := make([][]any, len(streams))
	shareOut(t, len(streams), goroutines, func(i int) error {
		decoder := gob.NewDecoder(bytes.NewReader(streams[i]))
		for {
			var e entry
			if err := decoder.Decode(&e); err == io.EOF {
				return nil
			} else if err != nil {
				return err
			}
			decoded[i] = append(decoded[i], e.kept())
		}
	})
}

// kept is what a load keeps of an entry.
func (e entry) kept() any {
	switch {
	case e.Task != nil:
		return tasks.Restore(*e.Task)
	case e.Summary != nil:
		return e.Summary
	}
	return agents.Restore(e.Roster)
}

// shareOut runs do for 0 to n-1 on goroutines goroutines, each taking every
// goroutines-th, and fails the test on the first error.
func shareOut(t *testing.T, n, goroutines int, do func(i int) error) {
	failed := make([]error, goroutines)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			for i := g; i < n && failed[g] == nil; i += goroutines {
				failed[g] = do(i)
			}
		})
	}
	wg.Wait()
	for _, err := range failed {
		if err != nil {
			t.Fatal(err)
		}
	}
}
