package coordinator

import (
	"strings"
	"testing"

	"gitlab.com/dev.chempa/agentry/internal/domain/agents"
)

// notesWithTasks are an agent's notes: two general points, then one section per
// earlier task, the way coordinators keep them.
const notesWithTasks = `How this repo works: Gradle for the backend, npm for the frontend.

## Working rules
- Read .agentry files with the Read tool only.

## task-46 (Health board build, 2026-10-24)
- The board reads check periods, never results older than 7 days.
- A long detail that only matters on task-46.

## epic-58 (paging)
- Stories 77 and 78 share SmsRouter.
`

func TestTheBriefCarriesGeneralNotesAndOnlyAnIndexOfTaskNotes(t *testing.T) {
	var b strings.Builder
	knowledge(&b, agents.Agent{Name: "tech-lead", Knowledge: notesWithTasks})
	brief := b.String()

	for _, want := range []string{
		"How this repo works: Gradle for the backend",
		"## Working rules",
		"Read .agentry files with the Read tool only.",
		"task-46 (Health board build, 2026-10-24)",
		"epic-58 (paging)",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief should carry %q:\n%s", want, brief)
		}
	}
	for _, notWant := range []string{
		"A long detail that only matters on task-46.",
		"Stories 77 and 78 share SmsRouter.",
	} {
		if strings.Contains(brief, notWant) {
			t.Errorf("an earlier task's note %q should stay in the file, not in the brief", notWant)
		}
	}
	if !strings.Contains(brief, "knowledge.md") {
		t.Errorf("the brief should say where the full notes are:\n%s", brief)
	}
	// The file is read back whole when the turn ends, so an agent that wrote
	// it out again from this brief would lose every earlier task's notes.
	if !strings.Contains(brief, "never write it out again from this brief") {
		t.Errorf("the brief should warn against rewriting the file from the brief:\n%s", brief)
	}
}

func TestNotesWithoutTaskSectionsAreCarriedWhole(t *testing.T) {
	notes := "One general point.\n\n## Working rules\n- Keep commits small.\n"
	var b strings.Builder
	knowledge(&b, agents.Agent{Name: "engineer", Knowledge: notes})
	if !strings.Contains(b.String(), "- Keep commits small.") {
		t.Errorf("notes with no task sections should be carried as they are:\n%s", b.String())
	}
}

// A section about a repo that names the task it came from is still about the
// repo, and holds guidance for every later task there.
func TestASectionThatOnlyMentionsATaskIsCarriedWhole(t *testing.T) {
	notes := "General.\n\n## Vesper repo (task-80 epic, phase 1)\n- Read docs/plan/phase-1-changes.md first.\n"
	var b strings.Builder
	knowledge(&b, agents.Agent{Name: "tech-lead", Knowledge: notes})
	if !strings.Contains(b.String(), "- Read docs/plan/phase-1-changes.md first.") {
		t.Errorf("a section whose heading only mentions a task should be carried whole:\n%s", b.String())
	}
}

func TestAHeadingInsideACodeBlockIsNotATaskSection(t *testing.T) {
	notes := "General.\n\n```\n## task-9 example output\nkept\n```\n"
	var b strings.Builder
	knowledge(&b, agents.Agent{Name: "engineer", Knowledge: notes})
	if !strings.Contains(b.String(), "kept") {
		t.Errorf("a heading inside a code block is text, not a section:\n%s", b.String())
	}
}
