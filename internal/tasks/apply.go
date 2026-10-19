package tasks

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/workers"
)

// FromEvents builds a task by applying its accepted events in order.
// It returns nil if the events don't include task.created, and panics with
// Replay's error for a stream it can't replay. Tests and callers that want
// a bad stream to fail loudly use it; code that must survive one, such as
// startup, uses Replay.
func FromEvents(stream []events.Event) *Task {
	t, err := Replay(stream)
	if err != nil {
		panic(err)
	}
	return t
}

// Replay is FromEvents with the error for a stream whose apply panics: the
// stream, position and event it stopped at. Apply is meant to replay every
// event the rules ever accepted, so the error is a bug, but one task's
// stream must not take down the reads of every other.
//
// An archived task folds to nil: at its task.archived the task so far is
// set aside, and Archived gives its summary. A task.unarchived takes back
// the task set aside, rebuilt from the restored rows before the marker in
// the same stream (docs/design/log-growth.md, "Reopening an archived
// task"); with none set aside the stream can't be replayed.
func Replay(stream []events.Event) (t *Task, err error) {
	var e events.Event
	defer func() {
		if recovered := recover(); recovered != nil {
			t, err = nil, fmt.Errorf("applying %s to %s at %d: %v", e.Type, e.Stream, e.Position, recovered)
		}
	}()
	var aside *Task
	for _, e = range stream {
		switch e.Type {
		case events.TaskCreated:
			t = &Task{ID: e.Stream, CreatedAt: e.At}
		case events.TaskArchived:
			t, aside = nil, t
			continue
		case events.TaskUnarchived:
			if aside == nil {
				panic("no task before its marker to take back")
			}
			t, aside = aside, nil
		}
		if t != nil {
			t.Apply(e)
		}
	}
	return t, nil
}

// Apply changes the task in place by one accepted event of its stream. A
// state loaded from a snapshot takes the events after it this way.
func (t *Task) Apply(e events.Event) {
	t.LastActiveAt = e.At
	if turn := t.Turn(e.Correlation); turn != nil {
		turn.LastEventAt = e.At
	}
	if action := t.Action(e.Correlation); action != nil {
		if turn := t.Turn(action.Turn); turn != nil {
			turn.LastEventAt = e.At
		}
	}

	t.applyFiles(e)
	if t.orphaned(e) {
		return
	}
	switch e.Type {
	case events.TaskCreated:
		t.Title, t.Description, t.Repo = e.Text("title"), e.Text("description"), e.Text("repo")
		t.Labels = e.List("labels")
		t.Budget, t.MaxTurns = e.Number("budget"), DefaultMaxTurns
		if _, given := e.Data["maxTurns"]; given { // a task made before keeps the old limit
			t.MaxTurns = e.Number("maxTurns")
		}
		t.Rank = -float64(e.Position) // newest first until you reorder; positions never repeat
		if e.Text("epic") != "" {
			t.Rank = float64(e.Position) // a story: after its epic's others, so a split keeps its planned order
		}
		t.Coordinator = e.Text("coordinator")
		t.TeamChosen, t.Parent, t.Depth = e.Flag("teamChosen"), e.Text("parent"), e.Number("depth")
		t.Kind, t.KindChosen, t.Epic, t.After = Kind(e.Text("kind")), e.Flag("kindChosen"), e.Text("epic"), e.List("after")
		t.Parked = e.Text("parked") // an epic's story, or an idea you accepted, starts in your Backlog
		t.MadeFrom, t.ByAgentry = MadeFromOf(e.Proposal), e.Actor.Kind == events.System
		t.About, t.AboutClose = e.Text("about"), int64(e.Number("close"))
		t.step(e, You, t.Coordinator, firstNonEmpty(t.Description, t.Title))

	case events.TaskDirected:
		// Only noted in the trail: the work in hand goes on, and the
		// coordinator reads it at its next turn.
		// An epic between stories has nothing in hand, so its coordinator
		// takes it up at once.
		if t.Kind == Epic && t.idle() {
			t.step(e, You, t.Coordinator, e.Text("text"))
		} else {
			t.Trail = append(t.Trail, Step{At: e.At, From: You, To: t.Coordinator, Text: e.Text("text")})
		}
		t.moveColumn(e)

	case events.TaskBudgeted:
		t.Budget = e.Number("budget")

	case events.TaskTurnsLimited:
		t.MaxTurns = e.Number("maxTurns")

	case events.TaskHeld:
		// Your move stands at once: a turn in progress stops, and the
		// coordinator wraps up. Moved on again, it carries on from there.
		place := e.Text("place")
		switch {
		case place != "" && t.Parked == "":
			t.Parked, t.Column = place, ""
			t.wrapUp(e, place, "I moved this task to "+PlaceName(place)+": it waits there until I move it on.")
		case place != "":
			t.Parked = place
		default: // a wrap-up still going is over: you want more work
			wrapping := t.WrapUp != nil
			t.Parked, t.WrapUp = "", nil
			t.AutoStarted = t.AutoStarted || e.Actor.Kind == events.System
			t.moveColumn(e)
			if t.stopTurn() || wrapping || t.idle() {
				t.step(e, You, t.Coordinator, "I moved this task back to work. Carry on from where it was.")
			}
		}

	case events.TaskWrappedUp:
		t.WrapUp, t.PendingDecision, t.Handoff, t.wrappedUp = nil, "", nil, len(t.Turns)
		t.reply(e)
		text := e.Text("summary")
		if e.Actor.Kind == events.Agent && t.IsDone {
			t.Summary = text
		}
		if text == "" {
			text = "Wrapped up without a summary: " + e.Text("reason")
		}
		t.Told = Step{At: e.At, From: e.Actor.ID, To: You, Text: text}
		t.Trail = append(t.Trail, t.Told)

	case events.TaskReopened:
		// Back to work in a fresh worktree (the loop makes it again), with a
		// new count of turns and issues, from what you said. Reopened while
		// its coordinator wraps up, the wrap-up turn stops, and the worktree,
		// not removed yet, is worked in again.
		if t.WrapUp != nil {
			t.stopTurn()
		} else {
			t.Worktree = nil
		}
		t.IsDone, t.DoneAt, t.DoneByYou, t.Summary, t.WrapUp = false, time.Time{}, false, "", nil
		t.Question, t.Asked, t.TurnsSinceAnswer, t.IssuesInARow = "", Asked{}, 0, 0
		t.TurnsBeforeReopen = len(t.Turns)
		t.step(e, You, t.Coordinator, e.Text("text"))
		t.moveColumn(e)

	case events.RepoChanged:
		// Its worktree is made again, of the new repo, before the next turn:
		// at once between turns, or when the turn in progress ends.
		repo, when := e.Text("repo"), " (it moves when the turn in progress ends)"
		t.MovingTo = repo
		if t.OpenTurn() == nil {
			t.moveRepo()
			when = ""
		}
		from, moved := You, "I moved this task"
		if e.Actor.Kind == events.Agent { // the coordinator, to the repo you named
			from, moved = t.Coordinator, "You moved this task, as the user asked,"
		}
		t.step(e, from, t.Coordinator, moved+" to the repo at "+repo+when+". From now on its work goes there: "+
			"the task has a new worktree of that repo, on its own branch, and every agent works in it.")

	case events.AgentRenamedInTask:
		// What the task does next goes to the agent by its new name; what
		// has happened keeps the name it had then.
		was, to := e.Text("name"), e.Text("to")
		if t.Coordinator == was {
			t.Coordinator = to
		}
		if t.Handoff != nil && t.Handoff.To == was {
			t.Handoff.To = to
		}

	case events.TaskTransferred:
		// Theirs from now on; the coordinator before passes it with why.
		was := t.Coordinator
		t.Coordinator, t.TeamChosen = e.Text("coordinator"), true
		t.step(e, was, t.Coordinator, "This task is your team's work: "+e.Text("reason"))

	case events.TaskRequested:
		// Not a handoff in this task: the other team works on its own task,
		// and this one waits for it.
		t.WaitingOn = e.Text("child")
		t.Trail = append(t.Trail, Step{At: e.At, From: t.Coordinator, To: e.Text("coordinator"),
			Text: "Asked the " + e.Text("team") + " team, as " + t.WaitingOn + ": " + e.Text("request")})
		t.PendingDecision, t.Handoff = "", nil

	case events.TaskSplit:
		t.Kind = Epic
		t.Stories = append(t.Stories, e.List("stories")...)
		t.reply(e)
		t.step(e, t.Coordinator, You, "Split into stories: "+e.Text("titles"))
		t.Told = Step{At: e.At, From: t.Coordinator, To: You,
			Text: "Split into stories: " + e.Text("titles") + ". They wait in your Backlog until you start them."}
		t.Closing = "" // more to do first
		t.hearStoryNews(e)
		t.hearApprovalNews(e)
	case events.EpicWaits:
		waits := "Waiting for its stories."
		if t.Closing != "" {
			waits = "Every story is done. It stays open until you close it or add a story."
		}
		t.reply(e)
		t.step(e, t.Coordinator, You, cmp.Or(e.Text("note"), waits))
		t.hearStoryNews(e)
		t.hearApprovalNews(e)
	case events.StoryDone:
		t.StoriesDone = append(t.StoriesDone, e.Text("story"))
		text := e.Text("story") + " is done: " + e.Text("summary")
		if t.idle() {
			t.step(e, e.Text("coordinator"), t.Coordinator, text)
		} else {
			// The coordinator is deciding: it hears this once it has.
			t.Trail = append(t.Trail, Step{At: e.At, From: e.Text("coordinator"), To: t.Coordinator, Text: text})
			t.storyNews = t.Handoff == nil
		}
	case events.EpicJoined:
		t.Epic = e.Text("epic")
	case events.EpicLeft:
		t.Epic, t.After = "", nil // what it built on were stories of that epic
	case events.StoryAdded:
		// Noted for the coordinator, which reads it at its next turn; the
		// work in hand goes on.
		story := e.Text("story")
		t.Stories = append(t.Stories, story)
		text := "I added " + story + " to this epic, as a story: " + e.Text("title")
		if e.Flag("done") {
			t.StoriesDone = append(t.StoriesDone, story)
			text += ". It is done already: " + e.Text("summary")
		} else {
			t.Closing = "" // more to do first
		}
		t.Trail = append(t.Trail, Step{At: e.At, From: You, To: t.Coordinator, Text: text})
	case events.StoryRemoved:
		story := e.Text("story")
		t.Stories = slices.DeleteFunc(t.Stories, func(id string) bool { return id == story })
		t.StoriesDone = slices.DeleteFunc(t.StoriesDone, func(id string) bool { return id == story })
		if len(t.Stories) == 0 {
			t.Closing = "" // no stories left to have finished
		}
		t.Trail = append(t.Trail, Step{At: e.At, From: You, To: t.Coordinator, Text: "I took " + story + " out of this epic: it is no longer one of its stories."})
	case events.FollowUpsSkipped:
		t.Skipped = int64(e.Number("close"))
		t.Trail = append(t.Trail, Step{At: e.At, From: Agentry, To: You, Text: e.Text("reason")})
	case events.RequestAnswered:
		t.WaitingOn = ""
		t.step(e, e.Text("coordinator"), t.Coordinator, e.Text("child")+" is done: "+e.Text("summary"))

	case events.TaskRanked:
		t.Rank = e.Real("rank")

	case events.TaskLabeled:
		t.Labels = e.List("labels")

	case events.TaskRenamed:
		if t.FirstTitle == "" {
			t.FirstTitle = t.Title
		}
		t.Title = e.Text("title")

	case events.WorktreeReady:
		t.Worktree = &Worktree{Repo: e.Text("repo"), Worker: cmp.Or(e.Text("worker"), workers.Local), Path: e.Text("path"),
			Branch: e.Text("branch"), Base: e.Text("base"), Port: e.Number("port")}

	case events.WorktreeMoved:
		t.Worktree.Worker, t.Worktree.Path, t.Worktree.Port = e.Text("worker"), e.Text("path"), e.Number("port")

	case events.TaskAssigned:
		// Its coordinator's handoff takes over the column you moved it to,
		// except a builder taking a task you put in Review, which a team
		// with no checker can do: it stays there. Moved to Shipping, the
		// handoff to an agent that ships work is the one your move asked for.
		ships := t.Column == "shipping" && e.Flag("ships")
		if t.Column != "review" || e.Flag("checks") {
			t.Column, t.SentBack = "", false
		}
		t.reply(e)
		t.step(e, e.Actor.ID, e.Text("agent"), e.Text("instructions"))
		t.Handoff.Machine, t.Handoff.Ships = e.Text("machine"), ships
		t.Trail[len(t.Trail)-1].Machine, t.Trail[len(t.Trail)-1].Ships = t.Handoff.Machine, ships

	case events.TurnStarted:
		t.Turns = append(t.Turns, &Turn{ID: e.Correlation, Agent: e.Text("agent"), Worker: cmp.Or(e.Text("worker"), workers.Local), Hash: e.Text("hash"),
			PlanFrom: e.Text("planFrom"), StartedAt: e.At, LastEventAt: e.At, Shipped: t.Handoff != nil && t.Handoff.Ships && t.Handoff.To == e.Text("agent")})
		t.Handoff = nil
		t.TurnsSinceAnswer++
		coordinates, unheard := e.Text("agent") == t.Coordinator, false
		if coordinates {
			unheard = t.hearApprovals(seenBy(e, len(t.Trail)))
		}
		t.startPasses(e.Correlation, e.Text("agent"))
		if coordinates {
			// Its brief has every story's news so far, and your approvals,
			// but those that came after it was made.
			t.storyNews, t.approvalNews = false, unheard
			if t.WrapUp != nil {
				t.WrapUp.Turn = e.Correlation
			}
		}

	case events.PromptAcknowledged:
		turn := t.Turn(e.Correlation)
		turn.Acknowledged, turn.Waiting = true, ""

	case events.ActionRequested:
		turn := t.OpenTurn()
		turn.Waiting, turn.RetriedAt = "", time.Time{} // working again after a retry
		t.Actions = append(t.Actions, &Action{ID: e.Correlation, Turn: turn.ID, Agent: e.Actor.ID,
			Tool: e.Text("tool"), Command: e.Text("command"), RequestedAt: e.At})
	case events.ActionAsked:
		action := t.Action(e.Correlation)
		action.Asked, action.Reason = true, e.Text("reason") // the rule that asked, until you decide
	case events.ActionApproved:
		t.decide(e, Approved)
	case events.ActionDenied:
		t.decide(e, Denied)
	case events.ActionHeld:
		action := t.Action(e.Correlation)
		action.Held, action.HeldAt = true, e.At
	case events.ActionResult:
		t.Action(e.Correlation).HasResult = true

	case events.PlanUpdated:
		t.Turn(e.Correlation).Plan = e.Text("plan")

	case events.AgentWaiting:
		t.Turn(e.Correlation).Waiting = e.Text("question")

	case events.TurnEnded:
		turn := t.Turn(e.Correlation)
		turn.Ended, turn.Report, turn.Tokens, turn.TokensUnknown = true, e.Text("report"), e.Number("tokens"), e.Text("tokensUnknown")
		t.moveRepo()
		t.TokensUsed += turn.Tokens
		t.IssuesInARow = 0
		if turn.Agent == t.Coordinator {
			// An empty reply is still one to decide on: it has no decision
			// in it, so it goes back to the coordinator as an issue.
			t.PendingDecision = cmp.Or(turn.Report, "(the coordinator's turn ended with no reply)")
		} else {
			t.step(e, turn.Agent, t.Coordinator, turn.Report)
		}

	case events.TurnLimited:
		// Not an issue: nothing is wrong with the work, so it doesn't count
		// towards asking you, and the turn stays open to go on after the
		// reset. An old log's odd one (an ended turn, a time it can't read)
		// changes what it can and nothing else.
		if turn := t.Turn(e.Correlation); !turn.Ended {
			again := turn.Limited != "" // the engine said so twice: the new reset time stands, noted once
			turn.Limited = cmp.Or(e.Text("details"), e.Text("error"), "usage limit")
			turn.ResetsAt, _ = time.Parse(time.RFC3339, e.Text("resetsAt"))
			if again {
				break
			}
			// Only noted in the trail: the turn goes on after the reset.
			t.Trail = append(t.Trail, Step{At: e.At, From: Agentry, To: t.Coordinator,
				Text: turn.Agent + "'s " + turn.ID + " hit a " + LimitText(turn.ResetsAt, e.At)})
		}

	case events.TurnResumed:
		// An old log's odd one (an ended turn, one not limited, another
		// agent's) changes nothing.
		if turn := t.Turn(e.Correlation); !turn.Ended && turn.Limited != "" && e.Text("agent") == turn.Agent {
			turn.Limited, turn.ResetsAt = "", time.Time{}
			turn.Resumes, turn.ResumedAt = turn.Resumes+1, e.At
			if e.Text("reason") == "reset" {
				turn.RetriedAt = e.At
			}
			t.Trail = append(t.Trail, Step{At: e.At, From: Agentry, To: t.Coordinator,
				Text: turn.Agent + "'s " + turn.ID + " resumed after its usage limit"})
		}

	case events.IssueRaised:
		t.Fetching = nil // a fetch that failed is told as an issue
		if turn := t.OpenTurn(); turn != nil {
			turn.Ended, turn.Issue = true, e.Text("reason")
			t.moveRepo()
		}
		t.IssuesInARow++
		t.step(e, Agentry, t.Coordinator, e.Text("reason"))

	case events.TurnStopped:
		// Not an issue: nothing went wrong, so it doesn't count towards
		// asking you. What you said is just above it in the trail.
		turn := t.Turn(e.Correlation)
		t.stopTurn()
		t.step(e, Agentry, t.Coordinator, stoppedText(turn, t.Coordinator))

	case events.TaskNeedsYou:
		t.Question = e.Text("question")
		t.Asked = Asked{By: e.Actor.ID, At: e.At, Options: e.List("options"), Page: e.Text("page"), Limit: e.Text("limit")}
		if e.Text("fetchPath") != "" {
			t.Asked.Fetch = &Fetch{Machine: e.Text("fetchMachine"), Path: e.Text("fetchPath"), As: e.Text("fetchAs")}
			t.Fetching = nil // it waits for you now
		}
		t.Closing = cmp.Or(e.Text("closing"), t.Closing)
		t.reply(e)
		t.step(e, e.Actor.ID, You, t.Question)
	case events.TaskAnswered:
		// Closing stays: an epic you leave open can still be closed later.
		t.Question, t.Asked, t.TurnsSinceAnswer, t.IssuesInARow = "", Asked{}, 0, 0
		t.step(e, You, t.Coordinator, e.Text("answer"))
		t.moveColumn(e)

	case events.TaskDone:
		t.IsDone, t.DoneAt, t.Summary = true, e.At, e.Text("summary")
		t.Question, t.Asked, t.Closing = "", Asked{}, "" // an epic you closed: that was your answer
		t.Column, t.SentBack = "", false
		// You moved it to Done on the board: it is done, whatever was in hand.
		if e.Flag("moved") {
			t.DoneByYou, t.Parked, t.WaitingOn = true, "", ""
			t.wrapUp(e, "done", "I moved this task to Done: it is finished as it is.")
			return
		}
		t.reply(e)
		t.step(e, e.Actor.ID, You, t.Summary)
	}
}

// orphaned is an event for a turn that isn't open or an action the task
// doesn't have. The rules refuse one now, but a log written under earlier
// rules can hold it (task.held didn't stop the turn then, so its actions
// went on), and replaying the log must not fail on it: it changes nothing.
func (t *Task) orphaned(e events.Event) bool {
	switch e.Type {
	case events.ActionRequested:
		return t.OpenTurn() == nil
	case events.ActionAsked, events.ActionApproved, events.ActionDenied, events.ActionHeld, events.ActionResult:
		return t.Action(e.Correlation) == nil
	case events.PromptAcknowledged, events.PlanUpdated, events.AgentWaiting, events.TurnEnded, events.TurnLimited, events.TurnResumed, events.TurnStopped:
		return t.Turn(e.Correlation) == nil
	}
	return false
}

// wrapUp makes your move to Done or a parking place stand at once: the turn
// in progress stops, and the coordinator has one last turn to wrap up, if
// anyone has worked on the task since it last wrapped up.
func (t *Task) wrapUp(e events.Event, to, text string) {
	if note := e.Text("note"); note != "" {
		text += " " + note
	}
	nothingNew := len(t.Turns) == t.wrappedUp // an open turn counts: it is stopped and wrapped up
	switch {
	case t.WrapUp != nil:
		// Parked, and moved to Done while it wraps up: the same turn now
		// wraps up a done task. (Done and then parked is reopened first,
		// which ends the wrap-up, so parking it owes a fresh one.)
		t.WrapUp.To = to
		t.Trail = append(t.Trail, Step{At: e.At, From: You, To: t.Coordinator, Text: text})
	case nothingNew && to == "done": // nothing to wrap up
		t.Trail = append(t.Trail, Step{At: e.At, From: You, To: t.Coordinator, Text: text})
		t.PendingDecision, t.Handoff = "", nil
	case nothingNew: // parked before it started, or since it wrapped up: it starts as it would have
	default:
		t.stopTurn()
		t.WrapUp = &WrapUp{To: to}
		t.step(e, You, t.Coordinator, text)
	}
}

// moveColumn records your move of the card to In progress, Review or
// Shipping, which the first event the move writes carries: the column stands
// at once.
func (t *Task) moveColumn(e events.Event) {
	if column := e.Text("column"); column != "" {
		t.Column, t.SentBack = column, e.Flag("sentBack")
	}
}

// stopTurn ends the turn in progress, if there is one, as you stopped it.
func (t *Task) stopTurn() bool {
	turn := t.OpenTurn()
	if turn == nil {
		return false
	}
	turn.Ended, turn.Stopped = true, true
	t.moveRepo()
	return true
}

func (t *Task) decide(e events.Event, decision Decision) {
	action := t.Action(e.Correlation)
	action.Decision, action.Reason = decision, e.Text("reason")
	if pass := e.Text("pass"); pass != "" {
		t.Action(pass).PassUsed = true
	}
	if action.Held && decision == Approved {
		t.handBack(e, action)
	}
}

// handBack tells the coordinator you approved a held action: its agent was
// told no long ago, so it runs it again only when the coordinator hands it
// back. Deciding, the coordinator hears it once it has decided.
func (t *Task) handBack(e events.Event, action *Action) {
	text := approvedLateText(t, action, e.At)
	action.HandBack = len(t.Trail)
	if t.idle() {
		t.step(e, Agentry, t.Coordinator, text)
		return
	}
	t.Trail = append(t.Trail, Step{At: e.At, From: Agentry, To: t.Coordinator, Text: text})
	t.approvalNews = t.approvalNews || t.Handoff == nil
}

// approvedLateText is the hand-back: who asked for what, when, and that the
// command is to be run again only once the branch is checked.
func approvedLateText(t *Task, action *Action, approvedAt time.Time) string {
	when := "after its turn ended"
	if turn := t.Turn(action.Turn); turn != nil && !turn.Ended {
		when = "while its turn went on without it"
	}
	again, turn := "Hand the task back to "+action.Agent+" to run it again", action.Agent+"'s next turn"
	if action.Agent == t.Coordinator {
		again, turn = "Run it again yourself in this turn", "this turn"
	}
	return fmt.Sprintf("The user approved a command %s asked for in %s at %s (%s), which Agentry held for them when no answer "+
		"came in time. They approved it at %s, %s, so %s was told no and hasn't run it. The full command:\n\n%s\n\n"+
		"%s, after checking the branch hasn't changed in a way that makes it wrong. Agentry lets this exact command "+
		"through once without asking, in %s only.",
		action.Agent, action.Turn, clock(action.RequestedAt), action.Tool, clock(approvedAt), when, action.Agent,
		action.Command, again, turn)
}

// hearApprovals marks the held actions you approved as heard when the
// starting coordinator turn's brief, made from the first seen steps of the
// trail, has their hand-back. It says whether one is still unheard: you
// approved it after the brief was made.
func (t *Task) hearApprovals(seen int) (unheard bool) {
	for _, action := range t.Actions {
		if action.Held && action.Decision == Approved && !action.Heard {
			action.Heard = action.HandBack < seen
			unheard = unheard || !action.Heard
		}
	}
	return unheard
}

// seenBy is how many steps of the trail the turn's brief was made from. A
// turn started before turn.started said so saw all of it.
func seenBy(e events.Event, trail int) int {
	if _, given := e.Data["seen"]; given {
		return e.Number("seen")
	}
	return trail
}

// startPasses gives the agent's held actions you approved, which its
// coordinator has heard and whose pass has no turn yet, this turn to work
// in: the only one their pass works in. A turn the coordinator gave the
// agent before it heard is for other work, and doesn't use the pass up.
func (t *Task) startPasses(turn, agent string) {
	for _, action := range t.Actions {
		if action.Held && action.Decision == Approved && action.Heard && action.PassTurn == "" && action.Agent == agent {
			action.PassTurn = turn
		}
	}
}

func clock(at time.Time) string { return at.UTC().Format("2006-01-02 15:04 UTC") }

// hearApprovalNews hands the task back to its coordinator when you approved
// a held action after its brief was made.
func (t *Task) hearApprovalNews(e events.Event) {
	if t.approvalNews {
		t.approvalNews = false
		t.step(e, Agentry, t.Coordinator, "The user approved a held command after your brief was made: see above. Decide again with it in mind.")
	}
}

// step adds a line to the trail. A step to an agent is also the task's
// pending handoff, and any step settles a pending decision.
// hearStoryNews hands the epic back to its coordinator when a story
// finished while it was deciding.
func (t *Task) hearStoryNews(e events.Event) {
	if t.storyNews {
		t.storyNews = false
		t.step(e, Agentry, t.Coordinator, "A story finished while you were deciding: see just above. Decide again with it in mind.")
	}
}

// idle is a task with nothing in hand: no turn, no decision to apply and
// no one about to start.
func (t *Task) idle() bool {
	return t.OpenTurn() == nil && t.PendingDecision == "" && t.Handoff == nil
}

func (t *Task) step(e events.Event, from, to, text string) {
	step := Step{At: e.At, From: from, To: to, Text: text}
	t.Trail = append(t.Trail, step)
	t.PendingDecision, t.Handoff = "", nil
	if to != You {
		t.Handoff = &step
	}
}

// reply adds the coordinator's answer to what you told it, which any
// decision may carry, to the trail before the decision itself.
func (t *Task) reply(e events.Event) {
	if text := e.Text("toYou"); text != "" {
		t.Told = Step{At: e.At, From: e.Actor.ID, To: You, Text: text}
		t.Trail = append(t.Trail, t.Told)
	}
}

// stoppedText tells the coordinator a turn was stopped for it to hear you.
func stoppedText(turn *Turn, coordinator string) string {
	if turn.Agent == coordinator {
		return "The user stopped your " + turn.ID + " to tell you what is just above. Decide again with it in mind."
	}
	return "The user stopped " + turn.Agent + "'s " + turn.ID + " to tell you what is just above. " + turn.Agent +
		" wrote no report, but its work so far is in the worktree, and its unfinished plan carries over to its next turn. " +
		"Unless what they said changes the direction, hand the task back to " + turn.Agent + " to carry on, with its " +
		"instructions brought up to date for what they said."
}

// LimitText says when a usage limit hit at at resets, in the server's local
// time: "usage limit, resets ~03:00", with the date when that isn't the day
// it was hit, or "usage limit, reset time unknown".
func LimitText(resetsAt, at time.Time) string {
	if resetsAt.IsZero() {
		return "usage limit, reset time unknown"
	}
	resets, hit := resetsAt.Local(), at.Local()
	layout := "15:04"
	if resets.Format(time.DateOnly) != hit.Format(time.DateOnly) {
		layout = "Jan 2 15:04"
	}
	return "usage limit, resets ~" + resets.Format(layout)
}

// moveRepo makes the move you asked for: the next turn works in a new
// worktree, of the new repo.
func (t *Task) moveRepo() {
	if t.MovingTo != "" {
		t.Repo, t.Worktree, t.MovingTo = t.MovingTo, nil, ""
	}
}

func turnID(number int) string { return fmt.Sprintf("turn-%d", number) }

// MadeFromOf is the stream a task.created says made the task, "" for none.
// Older logs and binaries name it only as "idea", so that stays the fallback.
func MadeFromOf(p events.Proposal) string {
	return cmp.Or(p.Text("from"), p.Text("idea"))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
