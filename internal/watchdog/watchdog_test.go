package watchdog

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/current"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/rules"
	"gitlab.com/dev.chempa/agentry/internal/store"
	"gitlab.com/dev.chempa/agentry/internal/storetest"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
)

var (
	you      = events.Actor{Kind: events.Human, ID: "you"}
	worker   = events.Actor{Kind: events.Worker, ID: "local"}
	policy   = events.Actor{Kind: events.Policy, ID: "rules"}
	engineer = events.Actor{Kind: events.Agent, ID: "engineer"}
)

// world is a store with task-1 in an open coordinator turn, and a fake
// process table: alive says whether the agent's process is running, except
// for the task named exited, whose never is. What is typed into the session
// goes to typed, after the events recorded by then; while held is open,
// typing waits for it to close, after closing entered if there is one.
type world struct {
	t         *testing.T
	store     *store.Store
	state     *current.State
	alive     bool
	exited    string
	stopped   bool
	typed     []string
	typedFrom []events.Type // the last event recorded when each line was typed
	typeErr   error
	held      chan struct{}
	entered   chan struct{}
	dog       *Watchdog
}

func newWorld(t *testing.T) *world {
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	return newWorldOn(t, s, state)
}

func newWorldOn(t *testing.T, s *store.Store, state *current.State) *world {
	w := &world{t: t, store: s, state: state, alive: true}
	typeLine := func(task, line string) error {
		if w.held != nil {
			if w.entered != nil {
				close(w.entered)
			}
			<-w.held
		}
		if w.typeErr != nil {
			return w.typeErr
		}
		stream, _ := w.store.Stream(task)
		w.typed, w.typedFrom = append(w.typed, line), append(w.typedFrom, stream[len(stream)-1].Type)
		return nil
	}
	isRunning := func(task string) bool { return w.alive && task != w.exited }
	w.dog = New(s, state, DefaultLimits, isRunning, func(string) { w.stopped = true }, typeLine)
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	w.propose(you, events.TaskCreated, "", map[string]any{"title": "t", "repo": "/r", "coordinator": "tech-lead"})
	w.propose(worker, events.WorktreeReady, "", map[string]any{"path": "/w", "branch": "b", "port": 7901})
	w.propose(worker, events.TurnStarted, "turn-1", map[string]any{"agent": "tech-lead", "hash": "h"})
	w.propose(worker, events.PromptAcknowledged, "turn-1", map[string]any{"hash": "h"})
	return w
}

func (w *world) propose(actor events.Actor, kind events.Type, correlation string, data map[string]any) {
	w.t.Helper()
	w.proposeOn("task-1", actor, kind, correlation, data)
}

func (w *world) proposeOn(stream string, actor events.Actor, kind events.Type, correlation string, data map[string]any) {
	w.t.Helper()
	if _, rejection, err := w.store.Propose(events.Proposal{Stream: stream, Actor: actor, Type: kind, Correlation: correlation, Data: data}); rejection != nil || err != nil {
		w.t.Fatal(kind, rejection, err)
	}
}

func (w *world) task() *tasks.Task { return w.taskOf("task-1") }

func (w *world) taskOf(id string) *tasks.Task {
	stream, _ := w.store.Stream(id)
	return tasks.FromEvents(stream)
}

// after runs the watchdog as if d had passed, and returns the issue it raised, or "".
func (w *world) after(d time.Duration) string {
	w.dog.Check(time.Now().Add(d))
	w.dog.Wait()
	if turn := w.task().LastTurn(); turn.Issue != "" {
		return turn.Issue
	}
	return ""
}

func TestAHealthyTurnIsLeftAlone(t *testing.T) {
	w := newWorld(t)
	if issue := w.after(5 * time.Minute); issue != "" || w.stopped {
		t.Errorf("issue %q, stopped %v", issue, w.stopped)
	}
}

// Proof 03: a crash fires no hook; only the dead pane shows it.
func TestACrashIsFlaggedAfterTheGrace(t *testing.T) {
	w := newWorld(t)
	w.alive = false
	if issue := w.after(time.Second); issue != "" {
		t.Errorf("within the start grace the process may not be up yet: %q", issue)
	}
	if issue := w.after(4 * time.Second); !strings.Contains(issue, "process exited") || !w.stopped {
		t.Errorf("issue %q, stopped %v", issue, w.stopped)
	}
	if w.task().OpenTurn() != nil {
		t.Error("the issue should end the turn, so the coordinator gets it")
	}
}

// An archived task is done and held as its summary alone, so the watchdog
// skips it as it skips a done task, though its process is gone.
func TestAnArchivedTaskIsLeftAlone(t *testing.T) {
	w := newWorld(t)
	w.alive = false
	storetest.Archive(t, w.store, w.state, "task-1")
	if wrote := w.proposedAfter(time.Hour); wrote != 0 || w.stopped {
		t.Errorf("wrote %d events, stopped %v", wrote, w.stopped)
	}
}

// proposedAfter runs the watchdog as if d had passed, and returns how many
// events it proposed, refused ones included: for an archived task, which
// has no turn left to read an issue from.
func (w *world) proposedAfter(d time.Duration) int64 {
	before := w.store.LastPosition()
	w.dog.Check(time.Now().Add(d))
	w.dog.Wait()
	return w.store.LastPosition() - before
}

func TestASilentTurnIsAStall(t *testing.T) {
	w := newWorld(t)
	if issue := w.after(11 * time.Minute); !strings.Contains(issue, "silent") {
		t.Errorf("issue %q", issue)
	}
}

func TestWaitingForYouIsNotAStall(t *testing.T) {
	w := newWorld(t)
	w.propose(worker, events.TurnEnded, "turn-1", map[string]any{"agent": "tech-lead", "report": "r"})
	w.propose(events.Actor{Kind: events.Agent, ID: "tech-lead"}, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"})
	w.propose(worker, events.TurnStarted, "turn-2", map[string]any{"agent": "engineer", "hash": "h2"})
	w.propose(worker, events.PromptAcknowledged, "turn-2", map[string]any{"hash": "h2"})
	w.propose(engineer, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "git push origin HEAD:dev"})
	w.propose(policy, events.ActionAsked, "toolu_1", map[string]any{"reason": "every push"})
	if issue := w.after(time.Hour); issue != "" {
		t.Errorf("an hour waiting for your approval is not a stall: %q", issue)
	}
}

func TestAToolOpenTooLong(t *testing.T) {
	w := newWorld(t)
	coordinator := events.Actor{Kind: events.Agent, ID: "tech-lead"}
	w.propose(coordinator, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "npm test"})
	w.propose(policy, events.ActionApproved, "toolu_1", map[string]any{"reason": "ok"})
	if issue := w.after(15 * time.Minute); issue != "" {
		t.Errorf("a running tool isn't silence, and 15 minutes is within its limit: %q", issue)
	}
	if issue := w.after(21 * time.Minute); !strings.Contains(issue, "npm test") {
		t.Errorf("issue %q", issue)
	}
}

func TestTheSameCommandRepeated(t *testing.T) {
	w := newWorld(t)
	coordinator := events.Actor{Kind: events.Agent, ID: "tech-lead"}
	for i := range DefaultLimits.Repeats {
		id := fmt.Sprintf("toolu_%d", i)
		w.propose(coordinator, events.ActionRequested, id, map[string]any{"tool": "Bash", "command": "go test ./..."})
		w.propose(policy, events.ActionApproved, id, map[string]any{"reason": "ok"})
		w.propose(worker, events.ActionResult, id, nil)
	}
	if issue := w.after(time.Second); !strings.Contains(issue, "5 times in a row") {
		t.Errorf("issue %q", issue)
	}
}

func TestAPromptThatNeverArrivesWaitsForYou(t *testing.T) {
	w := newWorld(t)
	w.propose(worker, events.TurnEnded, "turn-1", map[string]any{"agent": "tech-lead", "report": "r"})
	w.propose(events.Actor{Kind: events.Agent, ID: "tech-lead"}, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"})
	w.propose(worker, events.TurnStarted, "turn-2", map[string]any{"agent": "engineer", "hash": "h2"})

	w.after(61 * time.Second)
	if task := w.task(); task.Status() != tasks.NeedsYou || !strings.Contains(task.OpenTurn().Waiting, "trust") {
		t.Errorf("status %s, waiting %q", task.Status(), task.OpenTurn().Waiting)
	}
	w.propose(worker, events.PromptAcknowledged, "turn-2", map[string]any{"hash": "h2"})
	if task := w.task(); task.Status() != tasks.Working {
		t.Errorf("once the prompt arrives the task is working again, got %s", task.Status())
	}
}

// limit stops turn-1 at a usage limit that resets at resetsAt ("" when the
// engine didn't say).
func (w *world) limit(resetsAt string) {
	w.t.Helper()
	w.propose(worker, events.TurnLimited, "turn-1", map[string]any{"agent": "tech-lead", "error": "rate_limit", "details": "limit reached", "resetsAt": resetsAt})
}

// at is the time d from now, as the engine writes a reset time.
func at(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

func TestALimitedTurnIsRetriedOnceJustAfterTheReset(t *testing.T) {
	w := newWorld(t)
	w.limit(at(30 * time.Minute))
	if issue := w.after(30 * time.Minute); issue != "" || len(w.typed) != 0 || w.task().OpenTurn().Limited == "" {
		t.Fatalf("nothing is typed before the reset and its slack: issue %q, typed %q", issue, w.typed)
	}
	if issue := w.after(31*time.Minute + time.Second); issue != "" || w.stopped {
		t.Errorf("issue %q, stopped %v", issue, w.stopped)
	}
	if len(w.typed) != 1 || w.typed[0] != CarryOn {
		t.Fatalf("typed %q", w.typed)
	}
	if w.typedFrom[0] != events.TurnResumed {
		t.Errorf("the turn should be resumed before the prompt is typed, last event was %s", w.typedFrom[0])
	}
	stream, _ := w.store.Stream("task-1")
	if resumed := stream[len(stream)-1]; resumed.Type != events.TurnResumed || resumed.Text("reason") != "reset" || resumed.Actor != dog ||
		resumed.Correlation != "turn-1" || resumed.Text("agent") != "tech-lead" {
		t.Errorf("resumed %+v", resumed)
	}
	w.after(32 * time.Minute)
	w.after(33 * time.Minute)
	if len(w.typed) != 1 {
		t.Errorf("a retried turn is typed into once: %q", w.typed)
	}
}

// A task archived after its turn waited for a usage limit is done: nothing
// resumes it or types into it when the limit resets.
func TestALimitedTaskArchivedIsNotRetried(t *testing.T) {
	w := newWorld(t)
	w.limit(at(30 * time.Minute))
	storetest.Archive(t, w.store, w.state, "task-1")
	if wrote := w.proposedAfter(2 * time.Hour); wrote != 0 || w.stopped || len(w.typed) != 0 {
		t.Errorf("wrote %d events, stopped %v, typed %q", wrote, w.stopped, w.typed)
	}
	stream, _ := w.store.Stream("task-1")
	if last := stream[len(stream)-1]; last.Type != events.TaskArchived {
		t.Errorf("nothing should follow the marker, got %s", last.Type)
	}
}

func TestALimitWithNoResetTimeIsRetriedAfterAnHour(t *testing.T) {
	w := newWorld(t)
	w.limit("")
	if w.after(UnknownReset - time.Second); len(w.typed) != 0 {
		t.Errorf("typed %q", w.typed)
	}
	if w.after(UnknownReset + time.Second); len(w.typed) != 1 {
		t.Errorf("typed %q", w.typed)
	}
}

func TestALimitResetDaysAwayIsRetriedAfterADayAtMost(t *testing.T) {
	w := newWorld(t)
	w.limit(at(3 * 24 * time.Hour))
	if w.after(MaxLimitWait - time.Second); len(w.typed) != 0 {
		t.Errorf("typed %q", w.typed)
	}
	if w.after(MaxLimitWait + time.Second); len(w.typed) != 1 {
		t.Errorf("a reset days away is still retried after a day: typed %q", w.typed)
	}
}

func TestAPromptThatCannotBeTypedIsAnIssue(t *testing.T) {
	w := newWorld(t)
	w.limit("")
	w.typeErr = errors.New("no session is running")
	issue := w.after(UnknownReset + time.Second)
	if issue != "could not type the carry-on prompt after the usage limit: no session is running" || !w.stopped {
		t.Errorf("issue %q, stopped %v", issue, w.stopped)
	}
}

// moveOn ends turn-1 and starts turn-2, as when a resumed turn finishes
// while its carry-on prompt is still being typed.
func (w *world) moveOn() {
	w.t.Helper()
	w.propose(worker, events.TurnEnded, "turn-1", map[string]any{"agent": "tech-lead", "report": "r"})
	w.propose(worker, events.TurnStarted, "turn-2", map[string]any{"agent": "tech-lead", "hash": "h"})
}

func TestAPromptThatFailsAfterTheTurnMovedOnLeavesTheNewTurnAlone(t *testing.T) {
	w := newWorld(t)
	w.limit("")
	w.typeErr = errors.New("worker unreachable")
	w.held, w.entered = make(chan struct{}), make(chan struct{})
	w.dog.Check(time.Now().Add(UnknownReset + time.Second))
	<-w.entered
	w.moveOn()
	close(w.held)
	w.dog.Wait()
	if turn := w.task().OpenTurn(); turn == nil || turn.ID != "turn-2" || w.stopped {
		t.Errorf("turn-2 should still be open and its session running: open turn %+v, stopped %v", turn, w.stopped)
	}
}

func TestAPromptIsNotTypedIntoANewerTurn(t *testing.T) {
	w := newWorld(t)
	w.limit("")
	w.resume()
	w.moveOn()
	w.dog.carryOn("task-1", "turn-1")
	if len(w.typed) != 0 || w.stopped || w.task().OpenTurn() == nil {
		t.Errorf("typed %q, stopped %v, open turn %+v", w.typed, w.stopped, w.task().OpenTurn())
	}
}

// Typing into a remote worker's session can take half a minute, and the
// other tasks are checked meanwhile.
func TestAPromptBeingTypedHoldsUpNoOtherTask(t *testing.T) {
	w := newWorld(t)
	w.limit(at(30 * time.Minute))
	w.held = make(chan struct{})
	tick := func(now time.Time) {
		t.Helper()
		checked := make(chan struct{})
		go func() { w.dog.Check(now); close(checked) }()
		select {
		case <-checked:
		case <-time.After(5 * time.Second):
			close(w.held)
			t.Fatal("the check waited for the prompt to be typed")
		}
	}
	tick(time.Now().Add(31*time.Minute + time.Second))

	w.proposeOn("task-2", you, events.TaskCreated, "", map[string]any{"title": "t2", "repo": "/r", "coordinator": "tech-lead"})
	w.proposeOn("task-2", worker, events.WorktreeReady, "", map[string]any{"path": "/w2", "branch": "b2", "port": 7902})
	w.proposeOn("task-2", worker, events.TurnStarted, "turn-1", map[string]any{"agent": "tech-lead", "hash": "h"})
	w.exited = "task-2"
	tick(time.Now().Add(4 * time.Second))
	if issue := w.taskOf("task-2").LastTurn().Issue; !strings.Contains(issue, "process exited") {
		t.Errorf("the next tick should catch task-2's crash: %q", issue)
	}

	close(w.held)
	w.dog.Wait()
	if len(w.typed) != 1 || w.task().LastTurn().Issue != "" {
		t.Errorf("typed %q, task-1's issue %q", w.typed, w.task().LastTurn().Issue)
	}
}

func TestARetriedTurnQuietForTooLongIsAStall(t *testing.T) {
	w := newWorld(t)
	w.limit(at(30 * time.Minute))
	retried := time.Now().Add(31*time.Minute + time.Second)
	w.dog.Check(retried)
	w.dog.Wait()
	// The resume is stamped with the real clock, so silence counts from it.
	if issue := w.after(DefaultLimits.Silence + time.Minute); !strings.Contains(issue, "stalled") {
		t.Errorf("after the retry the turn is watched for silence as any other: %q", issue)
	}
}

func TestARefusedResumeTypesNothingAndStallsAfterTheReset(t *testing.T) {
	refuseResumes := func(p events.Proposal, state rules.State, catalogue *events.Catalogue) *events.Rejection {
		if p.Type == events.TurnResumed {
			return &events.Rejection{Reason: "refused for the test"}
		}
		return rules.Check(p, state, catalogue)
	}
	catalogue := events.Core()
	state := current.New(catalogue)
	s, err := store.Open(filepath.Join(t.TempDir(), "agentry.db"), catalogue, state, func(p events.Proposal) *events.Rejection { return refuseResumes(p, state, catalogue) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	w := newWorldOn(t, s, state)
	w.limit(at(30 * time.Minute))
	if issue := w.after(39 * time.Minute); issue != "" || len(w.typed) != 0 {
		t.Errorf("issue %q, typed %q", issue, w.typed)
	}
	if issue := w.after(42 * time.Minute); !strings.Contains(issue, "usage limit was due to reset") || len(w.typed) != 0 {
		t.Errorf("issue %q, typed %q", issue, w.typed)
	}
}

func TestALimitedTurnWithAToolStillOpenIsNotAStall(t *testing.T) {
	w := newWorld(t)
	coordinator := events.Actor{Kind: events.Agent, ID: "tech-lead"}
	w.propose(coordinator, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "npm test"})
	w.propose(policy, events.ActionApproved, "toolu_1", map[string]any{"reason": "ok"})
	w.limit(at(2 * time.Hour))
	if issue := w.after(DefaultLimits.Tool + DefaultLimits.Silence + time.Minute); issue != "" || w.stopped || len(w.typed) != 0 {
		t.Errorf("a limited turn waits for its reset, whatever was open: issue %q, stopped %v, typed %q", issue, w.stopped, w.typed)
	}
}

// A tool call still open at a usage limit is timed from the resume: the
// hours spent waiting for the reset aren't the tool's.
func TestAToolOpenAtALimitIsTimedFromTheResume(t *testing.T) {
	w := newWorld(t)
	coordinator := events.Actor{Kind: events.Agent, ID: "tech-lead"}
	w.propose(coordinator, events.ActionRequested, "toolu_1", map[string]any{"tool": "Bash", "command": "npm test"})
	w.propose(policy, events.ActionApproved, "toolu_1", map[string]any{"reason": "ok"})
	w.limit(at(2 * time.Hour))
	retried := time.Now().Add(2*time.Hour + time.Minute + time.Second)
	w.dog.Check(retried)
	w.dog.Wait()
	if issue := w.task().LastTurn().Issue; issue != "" || len(w.typed) != 1 {
		t.Fatalf("the turn should be retried at its reset: issue %q, typed %q", issue, w.typed)
	}

	// The store stamps events with the wall clock, so the resume is moved
	// to when the watchdog, running ahead, retried the turn.
	stream, _ := w.store.Stream("task-1")
	if resumed := &stream[len(stream)-1]; resumed.Type == events.TurnResumed {
		resumed.At = retried
	} else {
		t.Fatalf("the last event should be the resume, got %s", resumed.Type)
	}
	task := tasks.FromEvents(stream)
	for _, check := range []struct {
		at    time.Time
		issue bool
	}{
		{retried, false},
		{time.Now().Add(2*time.Hour + 2*time.Minute), false},
		{retried.Add(DefaultLimits.Tool - time.Second), false},
		{retried.Add(DefaultLimits.Tool + time.Second), true},
	} {
		issue := w.dog.findProblem(check.at, task, task.OpenTurn())
		if got := strings.Contains(issue, "tool call has run for over"); got != check.issue || !got && issue != "" {
			t.Errorf("%s after the retry: issue %q", check.at.Sub(retried), issue)
		}
	}
}

func TestALimitedTurnWhoseProcessExitedIsACrash(t *testing.T) {
	w := newWorld(t)
	w.limit(at(time.Hour))
	w.alive = false
	if issue := w.after(4 * time.Second); !strings.Contains(issue, "process exited") {
		t.Errorf("issue %q", issue)
	}
}

func TestADeadLimitedTurnIsACrashNotARetry(t *testing.T) {
	w := newWorld(t)
	w.limit(at(time.Minute))
	w.alive = false
	if issue := w.after(time.Hour); !strings.Contains(issue, "process exited") || len(w.typed) != 0 {
		t.Errorf("issue %q, typed %q", issue, w.typed)
	}
}

func TestALimitedTurnIsNeverAskedAboutItsPrompt(t *testing.T) {
	w := newWorld(t)
	w.propose(worker, events.TurnEnded, "turn-1", map[string]any{"agent": "tech-lead", "report": "r"})
	w.propose(events.Actor{Kind: events.Agent, ID: "tech-lead"}, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "go"})
	w.propose(worker, events.TurnStarted, "turn-2", map[string]any{"agent": "engineer", "hash": "h2"})
	w.propose(worker, events.TurnLimited, "turn-2", map[string]any{"agent": "engineer", "error": "rate_limit", "details": "", "resetsAt": ""})

	w.after(2 * time.Minute)
	if waiting := w.task().OpenTurn().Waiting; waiting != "" {
		t.Errorf("a limited engine echoes no prompt until the reset, so nobody should be asked: %q", waiting)
	}
}

func TestALimitedTurnSkipsTheRepeatCheck(t *testing.T) {
	w := newWorld(t)
	coordinator := events.Actor{Kind: events.Agent, ID: "tech-lead"}
	for i := range DefaultLimits.Repeats {
		id := fmt.Sprintf("toolu_%d", i)
		w.propose(coordinator, events.ActionRequested, id, map[string]any{"tool": "Bash", "command": "go test ./..."})
		w.propose(policy, events.ActionApproved, id, map[string]any{"reason": "ok"})
		w.propose(worker, events.ActionResult, id, nil)
	}
	w.limit("")
	if issue := w.after(time.Second); issue != "" {
		t.Errorf("issue %q", issue)
	}
}

// resume says turn-1's engine went on after its limit.
func (w *world) resume() {
	w.t.Helper()
	w.propose(worker, events.TurnResumed, "turn-1", map[string]any{"agent": "tech-lead", "reason": "engine"})
}

func TestAResumedTurnIsWatchedForSilenceAgain(t *testing.T) {
	w := newWorld(t)
	w.limit(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	w.resume()
	if issue := w.after(11 * time.Minute); !strings.Contains(issue, "stalled") {
		t.Errorf("a resumed turn quiet for over the silence limit is a stall, not waiting for the reset: %q", issue)
	}
}

func TestAResumedTurnCountsRepeatsAgain(t *testing.T) {
	w := newWorld(t)
	coordinator := events.Actor{Kind: events.Agent, ID: "tech-lead"}
	for i := range DefaultLimits.Repeats {
		id := fmt.Sprintf("toolu_%d", i)
		w.propose(coordinator, events.ActionRequested, id, map[string]any{"tool": "Bash", "command": "go test ./..."})
		w.propose(policy, events.ActionApproved, id, map[string]any{"reason": "ok"})
		w.propose(worker, events.ActionResult, id, nil)
	}
	w.limit("")
	w.resume()
	if issue := w.after(time.Second); !strings.Contains(issue, "5 times in a row") {
		t.Errorf("issue %q", issue)
	}
}
