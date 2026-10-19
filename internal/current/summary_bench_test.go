package current_test

import (
	"fmt"
	"runtime"
	"testing"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/ideas"
	"gitlab.com/dev.chempa/agentry/internal/storetest"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/tasks/taskstest"
)

// summaryHeapLimit is Target 5's term per archived task.
const summaryHeapLimit = 1000

// benchSummaries measures Target 5's term per archived task: the heap a
// state holds for a task that is its marker alone, as the archiver leaves
// it, keeping the summary of each archivable task. It is read as section 1
// read the heap: after runtime.GC, the state alive and the markers dropped.
func benchSummaries(t *testing.T, roster agents.Roster, archivable []*tasks.Task) {
	if len(archivable) == 0 {
		t.Log("  5 no task to archive, so no summary to measure")
		return
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	state := summariesOnly(roster, archivable)
	runtime.GC()
	runtime.ReadMemStats(&after)
	if listed := len(state.Summaries()); listed != len(archivable) {
		t.Errorf("the state lists %d summaries, want %d", listed, len(archivable))
	}
	held := float64(after.HeapAlloc-before.HeapAlloc) / float64(len(archivable))
	t.Logf("  5 heap per archived task <= %d B: %.0f B (%d summaries)", summaryHeapLimit, held, len(archivable))
	runtime.KeepAlive(state)
}

// summariesOnly is a state of the tasks archived: each one's stream is its
// marker alone.
func summariesOnly(roster agents.Roster, archivable []*tasks.Task) *current.State {
	state := current.New(storetest.WithIdeas(), ideas.Streams())
	for i, task := range archivable {
		created := events.Event{Position: int64(2*i + 1), Proposal: events.Proposal{Stream: task.ID}}
		marker := events.Event{Position: int64(2*i + 2), Proposal: taskstest.Marker([]events.Event{created}, tasks.SummaryOf(task, roster))}
		state.Load(task.ID, []events.Event{marker})
	}
	state.Loaded(int64(2 * len(archivable)))
	return state
}

// The summary heap measurement runs without AGENTRY_BENCH_DB, on a
// thousand copies of the task with every event: run with -v to see it.
func TestTheSummaryHeapBenchRunsOnAFixture(t *testing.T) {
	archivable := make([]*tasks.Task, 1000)
	for i := range archivable {
		archivable[i] = tasks.FromEvents(taskstest.EveryEvent())
		archivable[i].ID = fmt.Sprintf("task-%d", i+1)
	}
	benchSummaries(t, nil, archivable)
}
