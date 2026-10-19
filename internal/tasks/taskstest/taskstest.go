// Package taskstest holds logs for tests of tasks and of what keeps them.
package taskstest

import (
	"slices"
	"time"

	"gitlab.com/dev.chempa/agentry/internal/events"
)

// EveryEvent is a log of one task, task-1, that has every event a task
// applies.
func EveryEvent() []events.Event {
	you := events.Actor{Kind: events.Human, ID: "you"}
	lead := events.Actor{Kind: events.Agent, ID: "tech-lead"}
	engineer := events.Actor{Kind: events.Agent, ID: "engineer"}
	worker := events.Actor{Kind: events.Worker, ID: "local"}
	policy := events.Actor{Kind: events.Policy, ID: "policy"}
	agentry := events.Actor{Kind: events.System, ID: "loop"}
	add := func(log []events.Event, actor events.Actor, kind events.Type, correlation string, data map[string]any) []events.Event {
		at := time.Date(2026, 10, 18, 9, 0, len(log), 0, time.UTC)
		return append(log, events.Event{Position: int64(len(log) + 1), At: at,
			Proposal: events.Proposal{Stream: "task-1", Actor: actor, Type: kind, Correlation: correlation, Data: data}})
	}

	var log []events.Event
	log = add(log, you, events.TaskCreated, "", map[string]any{"title": "Speed up the list", "repo": "/r", "coordinator": "tech-lead",
		"labels": []any{"web"}, "budget": float64(1000), "after": []any{"task-0"}})
	log = add(log, you, events.TaskLabeled, "", map[string]any{"labels": []any{"web", "perf"}})
	log = add(log, lead, events.TaskRenamed, "", map[string]any{"title": "Make the list fast"})
	log = add(log, you, events.TaskBudgeted, "", map[string]any{"budget": float64(2000)})
	log = add(log, you, events.TaskRanked, "", map[string]any{"rank": 1.5})
	log = add(log, you, events.TaskHeld, "", map[string]any{"place": "todo"})
	log = add(log, you, events.TaskHeld, "", map[string]any{"place": ""})
	log = add(log, worker, events.WorktreeReady, "", map[string]any{"path": "/w", "branch": "agentry/task-1", "port": float64(7901)})
	log = add(log, worker, events.WorktreeMoved, "", map[string]any{"worker": "laptop", "path": "/l/w", "port": float64(7902)})
	log = add(log, you, events.TaskDirected, "", map[string]any{"text": "Measure first."})
	log = add(log, lead, events.TaskAssigned, "", map[string]any{"agent": "engineer", "instructions": "Profile it.", "machine": "laptop"})
	log = add(log, you, events.AgentRenamedInTask, "", map[string]any{"name": "engineer", "to": "builder"})
	log = add(log, worker, events.TurnStarted, "turn-1", map[string]any{"agent": "builder", "hash": "h"})
	log = add(log, worker, events.PromptAcknowledged, "turn-1", map[string]any{"hash": "h"})
	log = add(log, engineer, events.PlanUpdated, "turn-1", map[string]any{"plan": "- [ ] profile"})
	log = add(log, engineer, events.AgentWaiting, "turn-1", map[string]any{"question": "Which server?"})
	log = add(log, engineer, events.ActionRequested, "tool-1", map[string]any{"tool": "Bash", "command": "go test"})
	log = add(log, policy, events.ActionAsked, "tool-1", map[string]any{"reason": "ask rule"})
	log = add(log, you, events.ActionApproved, "tool-1", map[string]any{"reason": "fine"})
	log = add(log, worker, events.ActionResult, "tool-1", map[string]any{})
	log = add(log, engineer, events.ActionRequested, "tool-2", map[string]any{"tool": "Bash", "command": "rm x"})
	log = add(log, policy, events.ActionDenied, "tool-2", map[string]any{"reason": "no"})
	log = add(log, engineer, events.ActionRequested, "tool-3", map[string]any{"tool": "Bash", "command": "git push"})
	log = add(log, policy, events.ActionAsked, "tool-3", map[string]any{"reason": "ask rule"})
	log = add(log, policy, events.ActionHeld, "tool-3", nil)
	log = add(log, you, events.ActionApproved, "tool-3", map[string]any{"reason": "late"})
	log = add(log, engineer, events.ActionRequested, "tool-4", map[string]any{"tool": "Bash", "command": "git push"})
	log = add(log, policy, events.ActionApproved, "tool-4", map[string]any{"reason": "you approved this earlier", "pass": "tool-3"})
	log = add(log, agentry, events.FileAdded, "", map[string]any{"name": "profile.txt", "hash": "h1", "size": float64(10), "by": "builder"})
	log = add(log, agentry, events.FilesCollected, "", map[string]any{"turn": "turn-1"})
	log = add(log, worker, events.TurnLimited, "turn-1", map[string]any{"agent": "builder", "error": "rate_limit",
		"details": "You've hit your session limit", "resetsAt": "2026-10-20T03:00:00Z"})
	log = add(log, worker, events.TurnResumed, "turn-1", map[string]any{"agent": "builder", "reason": "engine"})
	log = add(log, worker, events.TurnEnded, "turn-1", map[string]any{"agent": "builder", "report": "Profiled.", "tokens": float64(300)})
	log = add(log, agentry, events.FilesDelivered, "", map[string]any{"worker": "laptop", "version": "1"})
	log = add(log, lead, events.FileFetching, "", map[string]any{"machine": "laptop", "path": "/db", "as": "db"})
	log = add(log, agentry, events.FilesFetched, "", map[string]any{"from": "laptop:/db",
		"files": []any{map[string]any{"name": "db", "hash": "h2", "size": float64(20)}}})
	log = add(log, you, events.FileRemoved, "", map[string]any{"name": "profile.txt"})
	log = add(log, lead, events.TaskNeedsYou, "", map[string]any{"question": "Copy the log?", "options": []any{"yes", "no"},
		"fetchMachine": "laptop", "fetchPath": "/log", "fetchAs": "log"})
	log = add(log, you, events.TaskAnswered, "", map[string]any{"answer": "yes", "asIs": true})
	log = add(log, you, events.RepoChanged, "", map[string]any{"repo": "/r2"})
	log = add(log, lead, events.TaskTransferred, "", map[string]any{"coordinator": "web-lead", "reason": "web work"})
	log = add(log, lead, events.TaskRequested, "", map[string]any{"child": "task-2", "team": "Design", "request": "a spinner", "coordinator": "designer"})
	log = add(log, agentry, events.RequestAnswered, "", map[string]any{"child": "task-2", "summary": "done", "coordinator": "designer"})
	log = add(log, lead, events.TaskSplit, "", map[string]any{"stories": []any{"task-3", "task-4"}, "titles": "a, b", "toYou": "Splitting."})
	log = add(log, agentry, events.StoryDone, "", map[string]any{"story": "task-3", "summary": "a done", "coordinator": "web-lead"})
	log = add(log, you, events.StoryAdded, "", map[string]any{"story": "task-5", "title": "c", "done": true, "summary": "c done"})
	log = add(log, you, events.StoryRemoved, "", map[string]any{"story": "task-4"})
	log = add(log, lead, events.EpicWaits, "", map[string]any{"note": "waiting"})
	log = add(log, you, events.EpicJoined, "", map[string]any{"epic": "task-9"})
	log = add(log, you, events.EpicLeft, "", map[string]any{"epic": "task-9"})
	log = add(log, worker, events.TurnStarted, "turn-2", map[string]any{"agent": "web-lead", "hash": "h"})
	log = add(log, agentry, events.IssueRaised, "", map[string]any{"reason": "the session died"})
	log = add(log, worker, events.TurnStarted, "turn-3", map[string]any{"agent": "builder", "hash": "h"})
	log = add(log, you, events.TurnStopped, "turn-3", map[string]any{})
	log = add(log, lead, events.TaskDone, "", map[string]any{"summary": "Fast now."})
	log = add(log, agentry, events.FollowUpsSkipped, "", map[string]any{"reason": "No Visionaries team.", "close": float64(40)})
	log = add(log, you, events.TaskReopened, "", map[string]any{"text": "One more thing."})
	log = add(log, lead, events.TaskDone, "", map[string]any{"summary": "Done again."})
	log = add(log, you, events.TaskReopened, "", map[string]any{"text": "And the board."})
	log = add(log, you, events.TaskTurnsLimited, "", map[string]any{"maxTurns": float64(5)})
	log = add(log, you, events.TaskDirected, "", map[string]any{"text": "Review it.", "column": "review"})
	log = add(log, worker, events.TurnStarted, "turn-4", map[string]any{"agent": "builder", "hash": "h"})
	log = add(log, you, events.TaskHeld, "", map[string]any{"place": "todo"}) // stops turn-4, the coordinator wraps up
	log = add(log, you, events.TaskHeld, "", map[string]any{"place": "backlog"})
	log = add(log, worker, events.TurnStarted, "turn-5", map[string]any{"agent": "web-lead", "hash": "h"})
	log = add(log, lead, events.TaskWrappedUp, "", map[string]any{"summary": "Wrapped."})
	log = add(log, you, events.TaskHeld, "", map[string]any{"place": "", "column": "in-progress"})
	log = add(log, worker, events.TurnStarted, "turn-6", map[string]any{"agent": "builder", "hash": "h"})
	log = add(log, you, events.TaskDone, "", map[string]any{"summary": "Moved.", "moved": true}) // stops turn-6, a wrap-up is owed
	return slices.Clip(log)
}

// Blob stands for an archive's hash in a test's markers.
const Blob = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Marker archives a task as the archiver will (docs/design/log-growth.md
// section 4): a task.archived covering accepted, the task's stream so far,
// keeping summary, which is what tasks.SummaryOf gave for it.
func Marker(accepted []events.Event, summary any) events.Proposal {
	return events.Proposal{Stream: accepted[0].Stream, Actor: events.Actor{Kind: events.System, ID: "archiver"}, Type: events.TaskArchived,
		Data: map[string]any{"through": accepted[len(accepted)-1].Position, "count": len(accepted), "archive": Blob,
			"created": accepted[0].Position, "summary": summary}}
}

// Unarchived undoes the marker at position marker, as Store.Unarchive will.
func Unarchived(stream string, marker int64) events.Proposal {
	return events.Proposal{Stream: stream, Actor: events.Actor{Kind: events.System, ID: "archiver"}, Type: events.TaskUnarchived,
		Data: map[string]any{"marker": marker, "archive": Blob}}
}
