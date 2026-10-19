// Package tasks computes a task's state from its events. The events are the
// only truth: a snapshot may store the state (Saved), but only as a cache
// that is thrown away whenever it might disagree with them, so the state
// never disagrees with what happened.
package tasks

import (
	"cmp"
	"strings"
	"time"
)

// Status is the one word the page shows for a task.
type Status string

const (
	Working  Status = "working"
	NeedsYou Status = "needs you"
	Done     Status = "done"
	Held     Status = "held" // parked in To do or the Backlog until you move it on
)

// Kind is what a task is. A story is one goal, done on one branch and
// checked before it is done; an epic is a goal too big for that, whose
// coordinator splits it into stories and finishes it once they are all done.
// Only two levels: a story has no stories of its own.
type Kind string

const (
	Story Kind = ""
	Epic  Kind = "epic"
)

type Task struct {
	ID          string
	Title       string
	FirstTitle  string // the title it was created with, when it has been renamed
	Labels      []string
	Budget      int     // tokens the task may use before it asks you; 0 is no limit
	MaxTurns    int     // turns the task may have without your answer before it asks you; 0 is no limit
	Rank        float64 // your priority order, lower first; a new task starts above every older one
	Parked      string  // "todo" or "backlog" while you hold it there; the coordinator leaves it alone
	Column      string  // "in-progress", "review" or "shipping": the board column you moved it to, until its coordinator next hands it on
	SentBack    bool    // that move took it from Review or Shipping back to In progress: it needs more work, not checking
	TokensUsed  int     // by every turn that has ended
	Description string
	Repo        string
	MovingTo    string  // a repo you moved the task to while a turn was running: it moves when the turn ends
	Coordinator string  // the agent that decides who works on the task next
	TeamChosen  bool    // you chose its team; otherwise the coordinator may pass it to the right one
	Parent      string  // the task that asked for this one, from another team; "" for yours
	Depth       int     // how many requests deep it is: yours are 0
	WaitingOn   string  // another team's task this one asked for and waits for, "" for none
	WrapUp      *WrapUp // you moved the task to Done, To do or the Backlog: its coordinator's last turn, until it is over
	wrappedUp   int     // its number of turns when its coordinator last wrapped up: only later ones need another
	MadeFrom    string  // the stream of whatever made the task, such as the idea you accepted, "" for none
	ByAgentry   bool    // Agentry made it, as for an idea it accepted for you, rather than you or an agent
	AutoStarted bool    // Agentry moved it out of the Backlog once already, to start it
	About       string  // for a Visionaries session Agentry started after you closed an epic: the epic, "" for any other task
	AboutClose  int64   // and the log position of that close: a reopened epic closed again is a new close

	Kind        Kind
	KindChosen  bool     // you chose story or epic; otherwise the coordinator may split a story that is too big
	Epic        string   // for a story of an epic: the epic
	After       []string // for a story: the epic's stories it builds on, which must be done before it starts
	Stories     []string // for an epic: its stories, in the order they were planned
	StoriesDone []string // for an epic: the stories whose summary came back
	Closing     string   // for an epic whose stories are all done: the summary it closes with, once you approve; kept while you leave it open
	Skipped     int64    // for an epic you closed: the close Agentry last said it started no Visionaries session for

	// Shared files (files.go).
	Files        map[string]SharedFile
	FilesVersion int             // goes up with every change to Files
	Delivered    string          // "machine@version": the folder last made to hold them
	Fetching     *Fetch          // a fetch the coordinator asked for, on its way
	collected    map[string]bool // the turns whose folder was read back
	storyNews    bool            // a story finished while the coordinator was busy deciding: it hears it next
	approvalNews bool            // you approved a held action while an agent's turn was open: the coordinator hears it next
	CreatedAt    time.Time

	Worktree *Worktree // nil until the worktree is ready
	Turns    []*Turn   // every agent turn, in order
	Actions  []*Action // every action an agent asked to take, in order
	Trail    []Step    // the handoffs, as the page shows them

	Told         Step   // the last thing a coordinator told you that is neither a question nor a summary: a reply, a split
	Question     string // an open question for the user, "" when there is none
	Asked        Asked  // how it was asked: by whom, when, with which options or page
	Summary      string // the coordinator's summary, once done
	IsDone       bool
	DoneAt       time.Time // when it was last done, zero while it isn't
	DoneByYou    bool      // you moved it to Done on the board, rather than its coordinator finishing it
	LastActiveAt time.Time

	TurnsBeforeReopen int // its number of turns when it was last reopened: only pushes in later ones count

	// What is waiting to happen. The coordinator loop acts on these.
	Handoff         *Step  // the latest step to an agent, until that agent's turn starts
	PendingDecision string // a coordinator's reply, until its decision is applied

	// For the loop's safety stops.
	TurnsSinceAnswer int // turns since the task started or you last answered
	IssuesInARow     int // issues with no normal turn end between them
}

// DefaultMaxTurns is the limit of turns of a task made before each task had
// its own: about 12 handoffs there and back, then you decide.
const DefaultMaxTurns = 24

// InPriorityOrder compares two tasks in your priority order: the lower rank
// first, and on a tie the newer first, as a new task starts above the rest.
// The board lists tasks and the loop gives them slots in this one order.
func InPriorityOrder(a, b *Task) int {
	return cmp.Or(cmp.Compare(a.Rank, b.Rank), b.CreatedAt.Compare(a.CreatedAt))
}

// SummariesInPriorityOrder is InPriorityOrder for the list's summaries.
func SummariesInPriorityOrder(a, b Summary) int {
	return cmp.Or(cmp.Compare(a.Rank, b.Rank), b.CreatedAt.Compare(a.CreatedAt))
}

// AtBudget says whether the task has used all the tokens it may.
func (t *Task) AtBudget() bool { return t.Budget > 0 && t.TokensUsed >= t.Budget }

// AtTurnLimit says whether the task has had all the turns it may without your answer.
func (t *Task) AtTurnLimit() bool { return t.MaxTurns > 0 && t.TurnsSinceAnswer >= t.MaxTurns }

// Loop is the coordinator loop's actor ID: a question it asks is one of its
// stops, not a coordinator's.
const Loop = "coordinator-loop"

// The limits that stop a task, as its question says.
const (
	LimitBudget = "budget"
	LimitTurns  = "turns"
)

// StoppedAt is the limit the task's open question is the loop's stop for,
// LimitBudget or LimitTurns, or "" for any other question: raising that
// limit answers it, and nothing else does.
func (t *Task) StoppedAt() string {
	switch {
	case t.Question == "" || t.Asked.By != Loop:
		return ""
	case t.Asked.Limit != "":
		return t.Asked.Limit
	case t.Asked.Fetch != nil:
		return "" // the copy question
	}
	// Asked before the question said which: the stops' fixed words tell, and
	// the loop's other questions, which carry no limit, never match them.
	switch {
	case strings.HasPrefix(t.Question, oldTurnStop):
		return LimitTurns
	case strings.HasPrefix(t.Question, oldBudgetStop):
		return LimitBudget
	}
	return ""
}

// How the loop's stops at the limits began before they said their limit.
const (
	oldTurnStop   = "This task has had "
	oldBudgetStop = "This task has used "
)

// WrapUp is the one turn a coordinator has after you move its task to Done,
// To do or the Backlog yourself: to write its summary, commit or tidy the
// work in progress, and note what was left. It starts no new work, and
// your move stands.
type WrapUp struct {
	To   string // where you moved it: "done", "todo" or "backlog"
	Turn string // the coordinator's wrap-up turn, once it has started
}

// ColumnName is a board column as the board names it: "Shipping".
func ColumnName(column string) string {
	names := map[string]string{"backlog": "Backlog", "todo": "To do", "in-progress": "In progress", "review": "Review", "shipping": "Shipping", "done": "Done"}
	return cmp.Or(names[column], column)
}

// PlaceName is the board's name for a place a task is parked in.
func PlaceName(place string) string {
	if place == "todo" {
		return "To do"
	}
	return "the Backlog"
}

type Worktree struct {
	Repo   string // the main checkout it was made from, on the machine Agentry runs on
	Worker string // the machine it is on: workers.Local, or a worker's name
	Path   string // on that machine
	Branch string
	Base   string // the commit the branch started from
	Port   int
}

// Turn is one agent working once, in its own session, from one prompt.
type Turn struct {
	ID            string // also the correlation of its events
	Agent         string
	Worker        string // the machine it ran on
	Hash          string // of the prompt line it was started with
	StartedAt     time.Time
	LastEventAt   time.Time
	Acknowledged  bool   // the engine echoed the prompt's hash (proof 02)
	Waiting       string // what the agent is waiting on, until it does something
	Plan          string // the agent's plan for the turn, a Markdown checklist, as it last wrote it
	PlanFrom      string // the turn whose unfinished plan this turn started with, "" for a fresh one
	Ended         bool
	Report        string    // the agent's final message
	Issue         string    // why the turn was cut short, if it was
	Limited       string    // what the engine said when it stopped at a usage limit, if it did
	ResetsAt      time.Time // when that limit resets, zero when the engine didn't say
	Resumes       int       // how many times it went on after a usage limit
	ResumedAt     time.Time // when it last went on after a usage limit
	RetriedAt     time.Time // when Agentry retried it at its limit's reset, until it next calls a tool
	Stopped       bool      // you ended it, to tell the coordinator something
	Shipped       bool      // it ships the work because you moved the task to Shipping: you took the work as it was
	Tokens        int       // what it used, when the engine said
	TokensUnknown string    // why Tokens is 0, when the engine couldn't say; "" in logs from before it was kept
}

type Decision string

const (
	Undecided Decision = ""
	Approved  Decision = "approved"
	Denied    Decision = "denied"
)

// Action is one tool call an agent asked to make, and what was decided.
type Action struct {
	ID          string // the engine's tool_use_id, the correlation of its events
	Turn        string
	Agent       string
	Tool        string
	Command     string
	RequestedAt time.Time
	Asked       bool // the policy handed it to the user
	Held        bool // no answer came before the hook gave up: it waited for you after its turn moved on
	HeldAt      time.Time
	Decision    Decision
	Reason      string // the rule that asked, then the reason for the decision
	HasResult   bool
	PassUsed    bool   // held and approved, its one pass was used: the same command asked for again went through
	HandBack    int    // held and approved, the place in the trail of the step that handed it back
	Heard       bool   // held and approved, a coordinator turn has started whose brief had its hand-back: only then can its pass start
	PassTurn    string // held and approved, its agent's first turn once heard: the only one its pass works in
}

// Step is one line of the handoff trail: who passed what to whom.
type Step struct {
	At      time.Time
	From    string
	To      string
	Text    string
	Machine string // for a handoff to an agent: where the coordinator wants it to work; "" lets Agentry choose
	Ships   bool   // a handoff to an agent that ships work, which your move to Shipping asked for
}

// Names used in the trail for the people and things that are not agents.
const (
	You     = "you"
	Agentry = "agentry"
)

func (t *Task) Status() Status {
	switch {
	case t.IsDone:
		return Done
	case t.Question != "" || len(t.WaitingForYou()) > 0:
		return NeedsYou
	case t.OpenTurn() != nil && t.OpenTurn().Waiting != "":
		return NeedsYou
	case t.Parked != "" && t.OpenTurn() == nil: // its coordinator may still be wrapping up
		return Held
	}
	return Working
}

// OpenTurn is the turn still running, or nil between turns.
func (t *Task) OpenTurn() *Turn {
	if len(t.Turns) == 0 || t.Turns[len(t.Turns)-1].Ended {
		return nil
	}
	return t.Turns[len(t.Turns)-1]
}

func (t *Task) LastTurn() *Turn {
	if len(t.Turns) == 0 {
		return nil
	}
	return t.Turns[len(t.Turns)-1]
}

func (t *Task) Turn(id string) *Turn {
	for _, turn := range t.Turns {
		if turn.ID == id {
			return turn
		}
	}
	return nil
}

// NextTurnID is the ID the next turn must have.
func (t *Task) NextTurnID() string {
	return turnID(len(t.Turns) + 1)
}

func (t *Task) Action(id string) *Action {
	for _, action := range t.Actions {
		if action.ID == id {
			return action
		}
	}
	return nil
}

// Pushed says whether the turn pushed the work: it made an approved git push
// that finished. A result comes from PostToolUse, which Claude fires only for
// a tool call that succeeded, so with Claude a denied or failed push never
// counts. Codex fires it for a command that exited non-zero too, with no exit
// code in the payload (codex-cli 0.163.0), so with Codex a push that failed
// may still count; a denied one never does. The prefix match relies on
// deployer.md giving the exact command, git push origin HEAD:dev, to run on
// its own: a push wrapped in timeout or chained after cd isn't seen.
func (t *Task) Pushed(turn string) bool {
	for _, action := range t.Actions {
		if action.Turn == turn && action.Decision == Approved && action.HasResult &&
			strings.HasPrefix(strings.TrimSpace(action.Command), "git push") {
			return true
		}
	}
	return false
}

// WaitingForYou are the actions the policy asked the user about, not yet decided.
// action.asked is the only way an action puts a task in Needs you (proof 05).
// A held action waits only while the task is at work: one done, or parked in
// To do or the Backlog, can't take a decision on it until it is moved back.
func (t *Task) WaitingForYou() []*Action {
	var waiting []*Action
	for _, action := range t.Actions {
		if action.Asked && action.Decision == Undecided && (!action.Held || t.AtWork()) {
			waiting = append(waiting, action)
		}
	}
	return waiting
}

// HooksWaiting are the asked actions a hook call is still waiting on: not
// decided, and not held.
func (t *Task) HooksWaiting() []*Action {
	var waiting []*Action
	for _, action := range t.Actions {
		if action.Asked && action.Decision == Undecided && !action.Held {
			waiting = append(waiting, action)
		}
	}
	return waiting
}

// AtWork is a task that is neither done nor parked.
func (t *Task) AtWork() bool { return !t.IsDone && t.Parked == "" }

// ApprovalUnheard says you approved a held action while the coordinator was
// deciding, and it hasn't heard yet.
func (t *Task) ApprovalUnheard() bool { return t.approvalNews }

// PassFor is the held action you approved whose pass lets this request
// through once without asking, or nil: the same agent asks for the same
// tool and command in its first turn after its coordinator heard your approval.
func (t *Task) PassFor(request *Action) *Action {
	for _, action := range t.Actions {
		if action.Held && action.Decision == Approved && !action.PassUsed && action.PassTurn == request.Turn &&
			action.Agent == request.Agent && action.Tool == request.Tool && action.Command == request.Command {
			return action
		}
	}
	return nil
}

// CurrentAgent is who has the task right now: the agent in the open turn, or "".
func (t *Task) CurrentAgent() string {
	if turn := t.OpenTurn(); turn != nil {
		return turn.Agent
	}
	return ""
}

// Asked is how the open question was put to you.
type Asked struct {
	By      string // the coordinator, or Agentry's loop
	At      time.Time
	Options []string // answers to pick from; you may still write your own
	Page    string   // a small HTML page the coordinator wrote to ask it, shown in a sandbox
	Fetch   *Fetch   // a file to copy from a machine, from outside its worktrees, which you approve
	Limit   string   // the limit that stopped the task, "budget" or "turns", "" for any other question
}
