package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gitlab.com/dev.chempa/agentry/internal/agents"
	"gitlab.com/dev.chempa/agentry/internal/events"
	"gitlab.com/dev.chempa/agentry/internal/storetest"
	"gitlab.com/dev.chempa/agentry/internal/tasks"
	"gitlab.com/dev.chempa/agentry/internal/workers"
)

// An archived task is in the list as its summary and still counts for the
// next id; its detail says it is archived, and no other reader shows it.
// task-2 waited for your answer with a worktree here before it was moved to
// Done and archived; the live tasks' summaries are as before.
func TestAnArchivedTaskIsListedAsItsSummary(t *testing.T) {
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	app := NewServer(s, state, Config{UIToken: token, DefaultRepo: "/r"}, http.NotFoundHandler(), nil)
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	for _, title := range []string{"One", "Two", "Three"} {
		call(t, server, "POST", "/api/tasks", `{"title":"`+title+`"}`)
	}
	for _, p := range []events.Proposal{
		{Stream: "task-2", Actor: events.Actor{Kind: events.Worker, ID: workers.Local}, Type: events.WorktreeReady,
			Data: map[string]any{"path": "/w/task-2", "branch": "b", "port": 7902}},
		{Stream: "task-2", Actor: events.Actor{Kind: events.Agent, ID: "tech-lead"}, Type: events.TaskNeedsYou,
			Data: map[string]any{"question": "Which layout?"}},
	} {
		if _, rejection, err := s.Propose(p); err != nil || rejection != nil {
			t.Fatal(p.Type, rejection, err)
		}
	}
	_, body := call(t, server, "GET", "/api/tasks", "")
	var before []taskSummary
	json.Unmarshal([]byte(body), &before)
	storetest.Archive(t, s, state, "task-2")
	archived := state.Summaries()[1] // task-2's: they are in the order the tasks were made

	_, body = call(t, server, "GET", "/api/tasks", "")
	var list []taskSummary
	json.Unmarshal([]byte(body), &list)
	var listed []string
	for _, summary := range list {
		listed = append(listed, summary.ID)
	}
	if !slices.Equal(listed, []string{"task-3", "task-2", "task-1"}) {
		t.Fatalf("the list shows %v, want task-3, task-2 and task-1", listed)
	}
	if !reflect.DeepEqual(list[0], before[0]) || !reflect.DeepEqual(list[2], before[2]) {
		t.Errorf("the live tasks' summaries changed:\n%+v\nwant\n%+v", list, before)
	}
	if list[1].Status != tasks.Done || list[1].Title != "Two" || !reflect.DeepEqual(list[1], archived) {
		t.Errorf("task-2 is listed as %+v, want its marker's summary %+v", list[1], archived)
	}
	if code, body := call(t, server, "GET", "/api/tasks/task-2", ""); code != http.StatusGone || !strings.Contains(body, "task-2 is archived") {
		t.Errorf("an archived task's detail: %d %s", code, body)
	}
	if code, body := call(t, server, "GET", "/api/tasks/task-2/events", ""); code != http.StatusGone || !strings.Contains(body, "task-2 is archived") {
		t.Errorf("an archived task's events: %d %s", code, body)
	}
	if _, body := call(t, server, "GET", "/api/questions", ""); strings.Contains(body, "task-2") {
		t.Errorf("an archived task asks nothing: %s", body)
	}
	if on := app.tasksByMachine()[workers.Local]; slices.Contains(on, "task-2") {
		t.Errorf("an archived task is on no machine: %v", on)
	}
	if busy, err := app.needing("tech-lead"); err != nil || !slices.Equal(busy, []string{"task-1", "task-3"}) {
		t.Errorf("tech-lead is needed by %v, %v: want task-1 and task-3", busy, err)
	}
	if code, body := call(t, server, "POST", "/api/tasks", `{"title":"Four"}`); code != 200 || !strings.Contains(body, `"stream":"task-4"`) {
		t.Errorf("the next task after an archived one: %d %s", code, body)
	}
	if code, body := call(t, server, "PUT", "/api/tasks/task-2/rank", `{"rank":1}`); code != http.StatusConflict || !strings.Contains(body, "task-2 is archived") {
		t.Errorf("an archived task took an event: %d %s", code, body)
	}
	// A rename tells every task the agent coordinates, a done one too; an
	// archived one takes no events, so it is skipped.
	if code, body := call(t, server, "POST", "/api/agents/tech-lead/rename", `{"to":"lead"}`); code != 200 {
		t.Errorf("renaming the archived task's coordinator: %d %s", code, body)
	}
}

// Closing an epic by moving it to Done parks its open stories and leaves
// an archived one, which is done, as it is.
func TestClosingAnEpicSkipsItsArchivedStory(t *testing.T) {
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	server := httptest.NewServer(NewServer(s, state, Config{UIToken: token, DefaultRepo: "/r"}, http.NotFoundHandler(), nil).Handler())
	defer server.Close()
	call(t, server, "POST", "/api/tasks", `{"title":"Shipped","kind":"story"}`)
	call(t, server, "POST", "/api/tasks", `{"title":"Not yet","kind":"story"}`)
	call(t, server, "POST", "/api/tasks", `{"title":"Ship it","stories":["task-1","task-2"]}`)
	if code, body := call(t, server, "POST", "/api/tasks/task-1/direct", `{"to":"done"}`); code != 200 {
		t.Fatalf("task-1 onto Done: %d %s", code, body)
	}
	storetest.Archive(t, s, state, "task-1")

	if code, body := call(t, server, "POST", "/api/tasks/task-3/direct", `{"to":"done"}`); code != 200 {
		t.Fatalf("the epic onto Done: %d %s", code, body)
	}
	if story, _ := state.Task("task-2"); story == nil || story.Parked != "backlog" {
		t.Errorf("the open story should be parked: %+v", story)
	}
}

// An archived task is done, so neither the list, a detail nor the epic's
// story list shows it blocking the story after it; the story after one that
// isn't done waits.
func TestAnArchivedTaskBlocksNoStory(t *testing.T) {
	s, state := storetest.Open(t, filepath.Join(t.TempDir(), "agentry.db"))
	for _, p := range agents.SeedProposals(nil) {
		s.Propose(p)
	}
	server := httptest.NewServer(NewServer(s, state, Config{UIToken: token, DefaultRepo: "/r"}, http.NotFoundHandler(), nil).Handler())
	defer server.Close()
	call(t, server, "POST", "/api/tasks", `{"title":"Shipped","kind":"story"}`)
	call(t, server, "POST", "/api/tasks", `{"title":"Not yet","kind":"story"}`)
	call(t, server, "POST", "/api/tasks", `{"title":"Ship it","stories":["task-1","task-2"]}`)
	if _, rejection, err := s.Propose(events.Proposal{Stream: "task-4", Actor: events.Actor{Kind: events.Human, ID: "you"}, Type: events.TaskCreated,
		Data: map[string]any{"title": "After both", "repo": "/r", "coordinator": "tech-lead", "kind": "", "epic": "", "after": []string{"task-1", "task-2"}}}); err != nil || rejection != nil {
		t.Fatal(rejection, err)
	}
	if code, body := call(t, server, "PUT", "/api/tasks/task-4/epic", `{"epic":"task-3"}`); code != 200 {
		t.Fatalf("task-4 into the epic: %d %s", code, body)
	}
	if code, body := call(t, server, "POST", "/api/tasks/task-1/direct", `{"to":"done"}`); code != 200 {
		t.Fatalf("task-1 onto Done: %d %s", code, body)
	}
	storetest.Archive(t, s, state, "task-1")

	_, body := call(t, server, "GET", "/api/tasks", "")
	var list []taskSummary
	json.Unmarshal([]byte(body), &list)
	at := slices.IndexFunc(list, func(summary taskSummary) bool { return summary.ID == "task-4" })
	if at < 0 {
		t.Fatalf("the list has no task-4: %s", body)
	}
	if !slices.Equal(list[at].BlockedBy, []string{"task-2"}) {
		t.Errorf("the list has task-4 blocked by %v, want only task-2", list[at].BlockedBy)
	}
	_, body = call(t, server, "GET", "/api/tasks/task-4", "")
	var detail taskDetail
	json.Unmarshal([]byte(body), &detail)
	if !slices.Equal(detail.BlockedBy, []string{"task-2"}) {
		t.Errorf("task-4's detail has it blocked by %v, want only task-2", detail.BlockedBy)
	}
	_, body = call(t, server, "GET", "/api/tasks/task-3", "")
	var epic taskDetail
	json.Unmarshal([]byte(body), &epic)
	at = slices.IndexFunc(epic.StoryList, func(summary taskSummary) bool { return summary.ID == "task-4" })
	if at < 0 {
		t.Fatalf("the epic's story list has no task-4: %s", body)
	}
	if !slices.Equal(epic.StoryList[at].BlockedBy, []string{"task-2"}) {
		t.Errorf("the epic's story list has task-4 blocked by %v, want only task-2", epic.StoryList[at].BlockedBy)
	}
}
