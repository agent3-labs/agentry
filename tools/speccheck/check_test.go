package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// A scenario that passes every rule, to break one rule at a time.
const goodScenario = `Feature: Tasks
  What the person at the page does.
  # Sources: internal/web and its tests.
  @TASK-01 @user @test
  Scenario: A new task goes to the coordinator
    Given a repo with a Software team
    When the user creates a task from the page
    Then the task is shown in To do
    And its coordinator is asked to decide
    # internal/web/api_test.go
`

// goodTestFile is the test file goodScenario cites, in the repo root.
const goodTestFile = "internal/web/api_test.go"

// writeSpec writes the files into a spec folder under a fresh temp folder,
// the repo root, and gives back that spec folder. A name starting with ../
// is a file of the repo outside the spec folder. goodTestFile is written
// too, unless the files give it.
func writeSpec(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "spec")
	if _, given := files["../"+goodTestFile]; !given {
		files = maps.Clone(files)
		files["../"+goodTestFile] = "package web\n"
	}
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// problemsOf runs the checker over the files and gives back each problem as
// "name:line: message", with the temp folder cut off.
func problemsOf(t *testing.T, files map[string]string) []string {
	t.Helper()
	dir := writeSpec(t, files)
	result, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, p := range result.Problems {
		rel, _ := filepath.Rel(dir, p.File)
		lines = append(lines, rel+":"+strconv.Itoa(p.Line)+": "+p.Message)
	}
	return lines
}

func expectProblems(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d problems, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			t.Errorf("problem %d is %q, want one holding %q", i+1, got[i], want[i])
		}
	}
}

func TestAScenarioCarriesItsFilesID(t *testing.T) {
	for name, c := range map[string]struct{ tags, want string }{
		"no ID":          {"@user @test", "tasks-and-board.feature:5: no ID tag; this file's scenarios are tagged @TASK-nn"},
		"another prefix": {"@HANDOFF-01 @user @test", "tasks-and-board.feature:5: ID @HANDOFF-01 does not match this file's prefix @TASK-nn"},
		"one digit":      {"@TASK-1 @user @test", "tasks-and-board.feature:5: no ID tag"},
		"two IDs":        {"@TASK-01 @TASK-02 @user @test", "tasks-and-board.feature:5: 2 ID tags; a scenario has one"},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(goodScenario, "@TASK-01 @user @test", c.tags, 1)
			expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}), c.want)
		})
	}
}

func TestAScenarioCarriesExactlyOneCheckTag(t *testing.T) {
	for name, c := range map[string]struct{ tags, want string }{
		"none": {"@TASK-01 @user", "tasks-and-board.feature:5: no check tag; a scenario has exactly one of @test, @benchmark, @manual"},
		"two":  {"@TASK-01 @user @test @manual", "tasks-and-board.feature:5: more than one check tag (@test, @manual); a scenario has exactly one"},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(goodScenario, "@TASK-01 @user @test", c.tags, 1)
			expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}), c.want)
		})
	}
}

func TestAFunctionalScenarioCarriesExactlyOneRoleTag(t *testing.T) {
	for name, c := range map[string]struct{ tags, want string }{
		"none":         {"@TASK-01 @test", "tasks-and-board.feature:5: no role tag; a functional scenario has exactly one of @user, @coordinator, @agent, @claude-code, @worker"},
		"two":          {"@TASK-01 @user @agent @test", "tasks-and-board.feature:5: more than one role tag (@user, @agent); a functional scenario has exactly one"},
		"@user":        {"@TASK-01 @user @test", ""},
		"@coordinator": {"@TASK-01 @coordinator @test", ""},
		"@agent":       {"@TASK-01 @agent @test", ""},
		"@claude-code": {"@TASK-01 @claude-code @test", ""},
		"@worker":      {"@TASK-01 @worker @test", ""},
		"another tag":  {"@TASK-01 @user @test @slow", ""},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(goodScenario, "@TASK-01 @user @test", c.tags, 1)
			got := problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text})
			if c.want == "" {
				expectProblems(t, got)
			} else {
				expectProblems(t, got, c.want)
			}
		})
	}
}

func TestANonFunctionalScenarioNeedsNoRoleTag(t *testing.T) {
	text := strings.NewReplacer("@TASK-01 @user @test", "@LIM-01 @test", "Tasks", "Limits").Replace(goodScenario)
	expectProblems(t, problemsOf(t, map[string]string{"non-functional/limits.feature": text}))
}

func TestOnlyAFunctionalFileNeedsSources(t *testing.T) {
	noSources := strings.Replace(goodScenario, "  # Sources: internal/web and its tests.\n", "\n", 1)
	limits := strings.NewReplacer("@TASK-01 @user @test", "@LIM-01 @test", "Tasks", "Limits").Replace(noSources)
	expectProblems(t, problemsOf(t, map[string]string{"non-functional/limits.feature": limits}))
	proposal := strings.Replace(noSources, "@TASK-01 @user @test", "@PROP-01 @user @test @to-tasks-and-board", 1)
	expectProblems(t, problemsOf(t, map[string]string{"proposals/board.feature": proposal}))
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": noSources}),
		"tasks-and-board.feature:1: no # Sources: line under the description")
}

func TestAnIDIsUsedOnceInTheFolder(t *testing.T) {
	twice := goodScenario + "\n" + strings.Replace(goodScenario, "Feature: Tasks\n  What the person at the page does.\n  # Sources: internal/web and its tests.\n", "", 1)
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": twice}),
		"tasks-and-board.feature:12: ID @TASK-01 is already used at ")
}

func TestAThenStatesANumberOrAnOutcome(t *testing.T) {
	for name, c := range map[string]struct {
		then  string
		wrong bool
	}{
		"vague":                  {"Then the task shows up", true},
		"a number":               {"Then the board shows 1 task", false},
		"an outcome word":        {"Then the task is shown on the board", false},
		"the word in any case":   {"Then NOT a thing changes", false},
		"a contraction":          {"Then the task doesn't move", true},
		"the word inside a word": {"Then the task is noted", true},
		"runs says nothing":      {"Then the page runs", true},
		"does says nothing":      {"Then the task does its work", true},
		"a cannot case":          {"Then the agent cannot push", false},
		"nothing happens":        {"Then nothing moves", false},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(goodScenario, "Then the task is shown in To do", c.then, 1)
			got := problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text})
			if c.wrong {
				expectProblems(t, got, "tasks-and-board.feature:8: Then has no number and no outcome word; the words are not, no, never")
			} else {
				expectProblems(t, got)
			}
		})
	}
}

func TestAnAndAfterAThenIsAThen(t *testing.T) {
	text := strings.Replace(goodScenario, "And its coordinator is asked to decide", "And its coordinator decides", 1)
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}),
		"tasks-and-board.feature:9: Then has no number and no outcome word")

	// An And after a Given or When is not a Then, and a When ends the Then.
	text = strings.Replace(goodScenario, "    When the user creates a task from the page\n",
		"    And the user is signed in\n    When the user creates a task from the page\n", 1)
	text = strings.Replace(text, "    # internal/web/api_test.go\n", "    When the user looks again\n    And the page is open\n", 1)
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}))
}

func TestVagueWordsAreRefused(t *testing.T) {
	for name, c := range map[string]struct{ old, new, want string }{
		"in a step":    {"Given a repo with a Software team", "Given the server starts quickly", `tasks-and-board.feature:6: the word "quickly"`},
		"in the title": {"Scenario: A new task goes to the coordinator", "Scenario: A task is handled Properly", `tasks-and-board.feature:5: the word "properly"`},
		"in a comment": {"# internal/web/api_test.go", "# checked appropriately", `tasks-and-board.feature:10: the word "appropriately"`},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(goodScenario, c.old, c.new, 1)
			expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}), c.want)
		})
	}
}

func TestAScenarioOutlineIsRefusedAndNotCounted(t *testing.T) {
	outline := goodScenario + `
  @TASK-02 @user @test
  Scenario Outline: A task ends up <outcome>
    Given a task
    When the coordinator decides
    Then the task is <outcome>

    Examples:
      | outcome |
      | done    |
`
	dir := writeSpec(t, map[string]string{"functional/tasks-and-board.feature": outline})
	result, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Files[0].Scenarios; got != 1 {
		t.Errorf("counted %d scenarios, want 1: the outline is not one", got)
	}
	if got := result.Tags[tagTest]; got != 1 {
		t.Errorf("counted %d @test, want 1: the outline's tags are not counted", got)
	}
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": outline}),
		"tasks-and-board.feature:13: Scenario Outline is not used in this spec: write one Scenario per case")
}

func TestABlankLineBetweenTheTagsAndTheScenarioIsExplained(t *testing.T) {
	text := strings.Replace(goodScenario, "@TASK-01 @user @test\n", "@TASK-01 @user @test\n\n", 1)
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}),
		"tasks-and-board.feature:6: no ID tag; this file's scenarios are tagged @TASK-nn; the tags must be on the line directly above Scenario:",
		"tasks-and-board.feature:6: no check tag; a scenario has exactly one of @test, @benchmark, @manual; the tags must be on the line directly above Scenario:",
		"tasks-and-board.feature:6: no role tag; a functional scenario has exactly one of @user, @coordinator, @agent, @claude-code, @worker; the tags must be on the line directly above Scenario:")

	// With no tag line above, the hint is not given.
	text = strings.Replace(goodScenario, "  @TASK-01 @user @test\n", "", 1)
	got := problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text})
	expectProblems(t, got, "tasks-and-board.feature:4: no ID tag", "tasks-and-board.feature:4: no check tag", "tasks-and-board.feature:4: no role tag")
	for _, p := range got {
		if strings.Contains(p, "directly above") {
			t.Errorf("hint given with no tag line above: %s", p)
		}
	}
}

func TestACommentBetweenTheTagsAndTheScenarioIsExplained(t *testing.T) {
	text := strings.Replace(goodScenario, "@TASK-01 @user @test\n", "@TASK-01 @user @test\n  # internal/web/api_test.go\n", 1)
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}),
		"tasks-and-board.feature:6: no ID tag; this file's scenarios are tagged @TASK-nn; the tags must be on the line directly above Scenario:",
		"tasks-and-board.feature:6: no check tag; a scenario has exactly one of @test, @benchmark, @manual; the tags must be on the line directly above Scenario:",
		"tasks-and-board.feature:6: no role tag; a functional scenario has exactly one of @user, @coordinator, @agent, @claude-code, @worker; the tags must be on the line directly above Scenario:")
}

func TestARejectedScenarioIsNotCountedUnderAnyCheckTag(t *testing.T) {
	text := goodScenario + "\n" + strings.NewReplacer("Feature: Tasks\n  What the person at the page does.\n\n", "",
		"@TASK-01 @user @test", "@TASK-02 @user @test @manual").Replace(goodScenario)
	dir := writeSpec(t, map[string]string{"functional/tasks-and-board.feature": text})
	result, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Problems) == 0 {
		t.Error("a scenario with two check tags gives no problem, so the run would not fail")
	}
	if got := result.Files[0].Scenarios; got != 2 {
		t.Errorf("counted %d scenarios, want 2", got)
	}
	if test, manual := result.Tags[tagTest], result.Tags[tagManual]; test != 1 || manual != 0 {
		t.Errorf("counted %d @test and %d @manual, want 1 and 0: the rejected scenario counts under neither", test, manual)
	}
}

func TestATestScenarioCitesPathsAndTestsThatExist(t *testing.T) {
	repo := map[string]string{
		"../" + goodTestFile:            "package web\n\nfunc TestCreate(t *testing.T) {}\n\nfunc TestList(t *testing.T) {}\n",
		"../internal/web/api.go":        "package web\n",
		"../internal/web/empty_test.go": "",
		"../ui/src/Badge.test.tsx":      "",
		"../README.md":                  "# Agentry\n",
	}
	for name, c := range map[string]struct{ tags, comment, want string }{
		"a path and its tests":       {"@TASK-01 @user @test", "# internal/web/api_test.go TestCreate and TestList", ""},
		"a missing path":             {"@TASK-01 @user @test", "# internal/web/gone_test.go", "tasks-and-board.feature:10: cited path internal/web/gone_test.go does not exist"},
		"a renamed test":             {"@TASK-01 @user @test", "# internal/web/api_test.go TestMake", "tasks-and-board.feature:10: TestMake is not defined in internal/web/api_test.go"},
		"a test with no path":        {"@TASK-01 @user @test", "# the list: TestList", "tasks-and-board.feature:10: TestList is cited with no Go test file before it"},
		"in the same file":           {"@TASK-01 @user @test", "# internal/web/api_test.go TestCreate; listing: TestList in the same file", ""},
		"gone from the file":         {"@TASK-01 @user @test", "# internal/web/api_test.go TestCreate; listing: TestGone in the same file", "tasks-and-board.feature:10: TestGone is not defined in internal/web/api_test.go"},
		"on the next line":           {"@TASK-01 @user @test", "# internal/web/api_test.go TestCreate\n    # the list: TestGone", "tasks-and-board.feature:11: TestGone is not defined in internal/web/api_test.go"},
		"a test before its path":     {"@TASK-01 @user @test", "# TestCreate in internal/web/api_test.go", ""},
		"a non-test file between":    {"@TASK-01 @user @test", "# internal/web/api_test.go TestCreate; the handler: internal/web/api.go\n    # TestList", ""},
		"a path with a line":         {"@TASK-01 @user @test", "# internal/web/api.go:42 and internal/web/api.go:3-5", ""},
		"a missing path with a line": {"@TASK-01 @user @test", "# internal/web/gone.go:42", "tasks-and-board.feature:10: cited path internal/web/gone.go does not exist"},
		"an empty test file":         {"@TASK-01 @user @test", "# internal/web/empty_test.go TestCreate", "tasks-and-board.feature:10: TestCreate is not defined in internal/web/empty_test.go"},
		"a UI path":                  {"@TASK-01 @user @test", "# ui/src/Badge.test.tsx, the badge's colour", ""},
		"a missing UI path":          {"@TASK-01 @user @test", "# ui/src/Gone.test.tsx", "tasks-and-board.feature:10: cited path ui/src/Gone.test.tsx does not exist"},
		"a glob":                     {"@TASK-01 @user @test", "# every page: ui/src/**/*.tsx", ""},
		"a root file":                {"@TASK-01 @user @test", "# README.md, Use it; NOTICE, the licences", "tasks-and-board.feature:10: cited path NOTICE does not exist"},
		"a path with a .. part":      {"@TASK-01 @user @test", "# internal/../internal/web/api.go", "tasks-and-board.feature:10: cited path internal/../internal/web/api.go isn't a path into the repo; name it from the repo root, with no empty, . or .. part"},
		"a path with an empty part":  {"@TASK-01 @user @test", "# internal//web/api.go", "tasks-and-board.feature:10: cited path internal//web/api.go isn't a path into the repo"},
		"a manual scenario":          {"@TASK-01 @user @manual", "# internal/web/gone_test.go TestGone", ""},
	} {
		t.Run(name, func(t *testing.T) {
			files := maps.Clone(repo)
			files["functional/tasks-and-board.feature"] = strings.NewReplacer("@TASK-01 @user @test", c.tags, "# internal/web/api_test.go", c.comment).Replace(goodScenario)
			got := problemsOf(t, files)
			if c.want == "" {
				expectProblems(t, got)
			} else {
				expectProblems(t, got, c.want)
			}
		})
	}
}

// The case tests prove something only on a case-insensitive filesystem, such
// as the Mac's, where os.Stat and os.ReadFile find internal/web for
// internal/WEB. On Linux they pass either way.
func TestATestCitationInTheWrongCaseFails(t *testing.T) {
	for name, c := range map[string]struct{ comment, want string }{
		"a folder in the wrong case": {
			"# internal/WEB/api_test.go TestCreate",
			"tasks-and-board.feature:10: cited path internal/WEB/api_test.go does not exist"},
		"a Go test file in the wrong case": {
			"# internal/web/Api_test.go TestCreate",
			"tasks-and-board.feature:10: cited path internal/web/Api_test.go does not exist"},
	} {
		t.Run(name, func(t *testing.T) {
			got := problemsOf(t, map[string]string{
				"../" + goodTestFile:                 "package web\n\nfunc TestCreate(t *testing.T) {}\n",
				"functional/tasks-and-board.feature": strings.Replace(goodScenario, "# internal/web/api_test.go", c.comment, 1),
			})
			expectProblems(t, got, c.want)
		})
	}
}

func TestATestFileInTheWrongCaseIsNotRead(t *testing.T) {
	dir := writeSpec(t, map[string]string{
		"../" + goodTestFile:                 "package web\n\nfunc TestCreate(t *testing.T) {}\n",
		"functional/tasks-and-board.feature": strings.Replace(goodScenario, "# internal/web/api_test.go", "# internal/web/Api_test.go TestCreate", 1),
	})
	result, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Tests != 0 {
		t.Errorf("counted %d Go tests, want 0: a wrong-case file is not read", result.Tests)
	}
}

func TestTheCitationsCheckedAreCounted(t *testing.T) {
	dir := writeSpec(t, map[string]string{
		"../" + goodTestFile:                 "package web\n\nfunc TestCreate(t *testing.T) {}\n",
		"functional/tasks-and-board.feature": strings.Replace(goodScenario, "# internal/web/api_test.go", "# internal/web/api_test.go TestCreate and TestGone", 1),
	})
	result, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Paths != 1 || result.Tests != 2 {
		t.Errorf("counted %d paths and %d Go tests, want 1 and 2", result.Paths, result.Tests)
	}
}

func TestANewScenarioCitesNoLineNumber(t *testing.T) {
	for name, c := range map[string]struct{ tags, comment, want string }{
		"a new ID with line":   {"@TASK-56 @user @test", "# internal/web/api_test.go, line 12", "tasks-and-board.feature:10: a line number goes stale as dev moves; cite a function or a test by name instead"},
		"a new ID with lines":  {"@TASK-56 @user @test", "# internal/web/api_test.go, lines 12 and 13", "tasks-and-board.feature:10: a line number goes stale"},
		"a new manual ID":      {"@TASK-56 @user @manual", "# internal/web/api.go line 12", "tasks-and-board.feature:10: a line number goes stale"},
		"an old ID with line":  {"@TASK-55 @user @test", "# internal/web/api_test.go, line 12", ""},
		"a new ID with a name": {"@TASK-56 @user @test", "# internal/web/api_test.go, the timeline 5 times", ""},
		"a capital Line":       {"@TASK-56 @user @test", "# internal/web/api_test.go, Line 12", "tasks-and-board.feature:10: a line number goes stale"},
		"a new ID with :N":     {"@TASK-56 @user @test", "# internal/web/api_test.go:42", "tasks-and-board.feature:10: a line number goes stale"},
		"a new ID with :N-M":   {"@TASK-56 @user @test", "# internal/web/api_test.go:3-5;", "tasks-and-board.feature:10: a line number goes stale"},
		"an old ID with :N":    {"@TASK-55 @user @test", "# internal/web/api_test.go:42", ""},
		"a time is not a path": {"@TASK-56 @user @test", "# internal/web/api_test.go, at 10:30", ""},
		"a capital Lines":      {"@TASK-56 @user @test", "# internal/web/api_test.go, Lines 3 to 5", "tasks-and-board.feature:10: a line number goes stale"},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.NewReplacer("@TASK-01 @user @test", c.tags, "# internal/web/api_test.go", c.comment).Replace(goodScenario)
			got := problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text})
			if c.want == "" {
				expectProblems(t, got)
			} else {
				expectProblems(t, got, c.want)
			}
		})
	}
}

func TestAStepMaySayLine(t *testing.T) {
	text := strings.NewReplacer("@TASK-01 @user @test", "@TASK-56 @user @test",
		"Then the task is shown in To do", "Then line 1 of the report is the verdict").Replace(goodScenario)
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}))

	text = strings.Replace(text, "# internal/web/api_test.go", "# internal/web/api_test.go, line 1 of the report is the verdict", 1)
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}),
		"tasks-and-board.feature:10: a line number goes stale")
}

func TestEveryRootListEntryIsInTheRepo(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range append(slices.Clone(rootFolders), rootFiles...) {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s is in the root lists but not at the repo root", name)
		}
	}
}

func TestACitationUnderAnUnlistedTopFolderFails(t *testing.T) {
	repo := map[string]string{
		"../scripts/build.sh": "",
		"../bin/agentry":      "",
		"../.gitignore":       "bin/\n",
	}
	for name, c := range map[string]struct{ comment, want string }{
		"an unlisted folder":  {"# internal/web/api_test.go; scripts/build.sh", "tasks-and-board.feature:10: cited path scripts/build.sh is under scripts/, a top folder rootFolders does not list; add scripts to rootFolders"},
		"a word with a slash": {"# internal/web/api_test.go, read and/or write", ""},
		"a top folder in the wrong case": {
			"# internal/web/api_test.go; Internal/web/api_test.go",
			"tasks-and-board.feature:10: cited path Internal/web/api_test.go does not exist"},
		"a misspelt top folder": {
			"# internal/web/api_test.go; intenal/web/api_test.go",
			"tasks-and-board.feature:10: cited path intenal/web/api_test.go does not exist"},
		"a word one letter off a short top folder": {"# internal/web/api_test.go, the gui/cli split", ""},
		"an ignored folder":                        {"# internal/web/api_test.go; built to bin/agentry", ""},
		"a dot folder":                             {"# internal/web/api_test.go; .agentry/files", ""},
	} {
		t.Run(name, func(t *testing.T) {
			files := maps.Clone(repo)
			files["functional/tasks-and-board.feature"] = strings.Replace(goodScenario, "# internal/web/api_test.go", c.comment, 1)
			got := problemsOf(t, files)
			if c.want == "" {
				expectProblems(t, got)
			} else {
				expectProblems(t, got, c.want)
			}
		})
	}
}

func TestAnEmDashAnywhereUnderTheFolderFails(t *testing.T) {
	expectProblems(t, problemsOf(t, map[string]string{
		"functional/tasks-and-board.feature": goodScenario,
		"README.md":                          "# The spec\n\nPlain prose \u2014 with a dash.\n",
	}), "README.md:3: em-dash")
}

func TestAnUnknownFeatureFileNameFails(t *testing.T) {
	expectProblems(t, problemsOf(t, map[string]string{"functional/users.feature": goodScenario}),
		"users.feature:1: unknown feature file name; the known names are agents.feature, approvals-and-questions.feature, claude-code-tools.feature")
}

func TestAGoodSpecPassesAndIsCounted(t *testing.T) {
	coordinator := `Feature: The coordinator
  # Sources: internal/web and its tests.
  @HANDOFF-01 @coordinator @manual
  Scenario: A decision hands the task to one agent
    Given a task whose coordinator has decided
    When the loop reads the decision
    Then exactly one agent gets a turn

  @HANDOFF-02 @agent @benchmark
  Scenario: Decisions are read fast
    Given 1000 decisions
    When they are read
    Then it takes under 1 second
`
	dir := writeSpec(t, map[string]string{
		"README.md":                                   "# The spec\n",
		"functional/tasks-and-board.feature":          goodScenario,
		"functional/coordinator-and-handoffs.feature": coordinator,
		"non-functional/limits.feature":               strings.NewReplacer("TASK", "LIM", " @user", "", "Tasks", "Limits").Replace(goodScenario),
	})
	t.Chdir(filepath.Dir(dir))
	if _, err := Check("nowhere"); err == nil {
		t.Error("a folder that is not there should be an error")
	}
	result, err := Check("spec")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("a good spec has problems: %+v", result.Problems)
	}
	var out bytes.Buffer
	printCounts(&out, result)
	want := `spec/functional/coordinator-and-handoffs.feature: 2 scenarios
spec/functional/tasks-and-board.feature: 1 scenario
spec/non-functional/limits.feature: 1 scenario
3 files, 4 scenarios: 2 @test, 1 @benchmark, 1 @manual
proposals: 0 files, 0 scenarios: 0 @test, 0 @benchmark, 0 @manual
cited by @test: 2 paths, 0 Go tests
`
	if out.String() != want {
		t.Errorf("counts:\n%s\nwant:\n%s", out.String(), want)
	}
}

// goodProposal is goodScenario as a proposal: a @PROP ID and a target tag.
var goodProposal = strings.Replace(goodScenario, "@TASK-01 @user @test", "@PROP-01 @user @test @to-tasks-and-board", 1)

func TestAProposalCarriesExactlyOneKnownTarget(t *testing.T) {
	for name, c := range map[string]struct{ tags, want string }{
		"none":                  {"@PROP-01 @user @test", "proposals/board.feature:5: no target tag; a proposal has exactly one @to-<stem>, naming the file it moves to when built: the known stems are agents, approvals-and-questions,"},
		"two":                   {"@PROP-01 @user @test @to-policy @to-security", "proposals/board.feature:5: more than one target tag (@to-policy, @to-security); a proposal has exactly one"},
		"an unknown stem":       {"@PROP-01 @user @test @to-billing", "proposals/board.feature:5: unknown target @to-billing; the known stems are agents, approvals-and-questions,"},
		"a stem with .feature":  {"@PROP-01 @user @test @to-policy.feature", "proposals/board.feature:5: unknown target @to-policy.feature;"},
		"a capital new stem":    {"@PROP-01 @user @test @to-new-Output-gate", "proposals/board.feature:5: unknown target @to-new-Output-gate; the known stems are agents, approvals-and-questions, claude-code-tools, compatibility, coordinator-and-handoffs, demo, epics-and-stories, ideas, limits, notifications, observability, operations, performance, policy, reliability, schedules, security, sessions, settings, shared-files, sign-in, tasks-and-board, teams-and-templates, usability, workers, worktrees-and-branches, or new-<stem> in lower-case words joined by dashes for a file that does not exist yet"},
		"a functional file":     {"@PROP-01 @user @test @to-policy", ""},
		"a non-functional file": {"@PROP-01 @test @to-security", ""},
		"a new file":            {"@PROP-01 @test @to-new-output-gate", ""},
		"a new file and role":   {"@PROP-01 @agent @test @to-new-output-gate", ""},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(goodProposal, "@PROP-01 @user @test @to-tasks-and-board", c.tags, 1)
			got := problemsOf(t, map[string]string{"proposals/board.feature": text})
			if c.want == "" {
				expectProblems(t, got)
			} else {
				expectProblems(t, got, c.want)
			}
		})
	}
}

func TestAProposalCarriesItsTargetsRoleTag(t *testing.T) {
	for name, c := range map[string]struct{ tags, want string }{
		"functional, none":        {"@PROP-01 @test @to-policy", "proposals/board.feature:5: no role tag; a functional scenario has exactly one of @user, @coordinator, @agent, @claude-code, @worker (@to-policy is a functional file)"},
		"functional, two":         {"@PROP-01 @user @agent @test @to-policy", "proposals/board.feature:5: more than one role tag (@user, @agent); a functional scenario has exactly one (@to-policy is a functional file)"},
		"non-functional, one":     {"@PROP-01 @user @test @to-security", "proposals/board.feature:5: role tag @user on a proposal for a non-functional file; a non-functional scenario carries none (@to-security is a non-functional file)"},
		"non-functional, two":     {"@PROP-01 @user @agent @test @to-security", "proposals/board.feature:5: role tags @user, @agent on a proposal for a non-functional file; a non-functional scenario carries none"},
		"non-functional, none":    {"@PROP-01 @test @to-security", ""},
		"a new file, two":         {"@PROP-01 @user @agent @test @to-new-output-gate", "proposals/board.feature:5: more than one role tag (@user, @agent); a proposal for a new file has one or none"},
		"an unknown target, none": {"@PROP-01 @test @to-billing", "proposals/board.feature:5: unknown target @to-billing"},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(goodProposal, "@PROP-01 @user @test @to-tasks-and-board", c.tags, 1)
			got := problemsOf(t, map[string]string{"proposals/board.feature": text})
			if c.want == "" {
				expectProblems(t, got)
			} else {
				expectProblems(t, got, c.want)
			}
		})
	}
}

func TestAProposalCarriesAPropID(t *testing.T) {
	for name, c := range map[string]struct{ tags, want string }{
		"no ID":        {"@user @test @to-policy", "proposals/board.feature:5: no ID tag; this file's scenarios are tagged @PROP-nn"},
		"a file's ID":  {"@POLICY-01 @user @test @to-policy", "proposals/board.feature:5: ID @POLICY-01 does not match this file's prefix @PROP-nn"},
		"no check tag": {"@PROP-01 @user @to-policy", "proposals/board.feature:5: no check tag; a scenario has exactly one of @test, @benchmark, @manual"},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(goodProposal, "@PROP-01 @user @test @to-tasks-and-board", c.tags, 1)
			expectProblems(t, problemsOf(t, map[string]string{"proposals/board.feature": text}), c.want)
		})
	}
}

func TestAProposalCitesNoLineNumber(t *testing.T) {
	text := strings.Replace(goodProposal, "# internal/web/api_test.go", "# internal/domain/policy/rules.go, line 12", 1)
	expectProblems(t, problemsOf(t, map[string]string{"proposals/board.feature": text}),
		"proposals/board.feature:10: a line number goes stale")
}

func TestAProposalMayNameATestThatDoesNotExistYet(t *testing.T) {
	missing := func(text string) string {
		return strings.Replace(text, "# internal/web/api_test.go", "# internal/gate/output_test.go TestAResultIsHeld", 1)
	}
	expectProblems(t, problemsOf(t, map[string]string{"proposals/output-gate.feature": missing(goodProposal)}))

	// The same scenario under functional/ still fails.
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": missing(goodScenario)}),
		"tasks-and-board.feature:10: cited path internal/gate/output_test.go does not exist")
}

func TestAPropIDIsUsedOnceInTheFolder(t *testing.T) {
	expectProblems(t, problemsOf(t, map[string]string{
		"proposals/a-gate.feature": goodProposal,
		"proposals/b-gate.feature": goodProposal,
	}), "proposals/b-gate.feature:4: ID @PROP-01 is already used at ")
}

func TestProposalsAreTotalledApartFromTheBaseline(t *testing.T) {
	second := strings.NewReplacer("Feature: Tasks\n  What the person at the page does.\n\n", "",
		"@PROP-01 @user @test", "@PROP-02 @user @manual").Replace(goodProposal)
	dir := writeSpec(t, map[string]string{
		"functional/tasks-and-board.feature": goodScenario,
		"proposals/output-gate.feature":      goodProposal + "\n" + second,
	})
	t.Chdir(filepath.Dir(dir))
	result, err := Check("spec")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("a good spec has problems: %+v", result.Problems)
	}
	var out bytes.Buffer
	printCounts(&out, result)
	want := `spec/functional/tasks-and-board.feature: 1 scenario
spec/proposals/output-gate.feature: 2 scenarios
1 file, 1 scenario: 1 @test, 0 @benchmark, 0 @manual
proposals: 1 file, 2 scenarios: 1 @test, 0 @benchmark, 1 @manual
cited by @test: 1 path, 0 Go tests
`
	if out.String() != want {
		t.Errorf("counts:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestOnlyTheTopProposalsFolderHoldsProposals(t *testing.T) {
	// A folder named proposals under functional/ is not the proposals folder:
	// its file is judged by its name, as any other, and counted in the baseline.
	files := map[string]string{"functional/proposals/board.feature": goodProposal}
	expectProblems(t, problemsOf(t, files),
		"functional/proposals/board.feature:1: unknown feature file name",
		"functional/proposals/board.feature:5: target tag @to-tasks-and-board belongs only in a file directly under spec/proposals; a built scenario drops it")
	result, err := Check(writeSpec(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if result.Files[0].Proposal || result.ProposalTags[tagTest] != 0 || result.Tags[tagTest] != 1 {
		t.Errorf("functional/proposals/board.feature is counted as a proposal: %+v", result)
	}
	expectProblems(t, problemsOf(t, map[string]string{"proposals/sub/board.feature": goodProposal}),
		"proposals/sub/board.feature:1: unknown feature file name",
		"proposals/sub/board.feature:5: target tag @to-tasks-and-board belongs only in a file directly under spec/proposals; a built scenario drops it")
}

func TestATargetTagOutsideProposalsFails(t *testing.T) {
	text := strings.Replace(goodScenario, "@TASK-01 @user @test", "@TASK-01 @user @test @to-policy", 1)
	expectProblems(t, problemsOf(t, map[string]string{"functional/tasks-and-board.feature": text}),
		"tasks-and-board.feature:5: target tag @to-policy belongs only in a file directly under spec/proposals; a built scenario drops it")
}

func TestEveryPathAFeaturesSourcesNamesExists(t *testing.T) {
	repo := map[string]string{
		// No Go file, so no package that every case would have to name.
		"../internal/domain/tasks/README.md": "",
		"../ui/src/pages/TaskPage.tsx":       "",
		"../README.md":                       "# Agentry\n",
		"../scripts/build.sh":                "",
	}
	feature := func(sources string) string {
		return strings.Replace(goodScenario, "  # Sources: internal/web and its tests.\n", sources+"\n", 1)
	}
	for name, c := range map[string]struct{ sources, want string }{
		"no Sources": {"", "tasks-and-board.feature:1: no # Sources: line under the description"},
		"a good sentence over lines": {
			"  # Sources: internal/domain/tasks, internal/web/api_test.go,\n  # ui/src/pages/TaskPage.tsx, README.md and their\n  # tests.\n", ""},
		"a missing path": {
			"  # Sources: internal/domain/tasks, internal/nope and their tests.\n",
			"tasks-and-board.feature:3: Sources names internal/nope, which doesn't exist"},
		"a missing path on a later line": {
			"  # Sources: internal/domain/tasks,\n  # ui/src/pages/Gone.tsx and its tests.\n",
			"tasks-and-board.feature:4: Sources names ui/src/pages/Gone.tsx, which doesn't exist"},
		"a missing file in a folder that exists": {
			"  # Sources: internal/domain/tasks/gone.go and its tests.\n",
			"tasks-and-board.feature:3: Sources names internal/domain/tasks/gone.go, which doesn't exist"},
		"the full stop ends it": {
			"  # Sources: internal/domain/tasks and their tests.\n  # internal/nope is a later comment, not a source\n", ""},
		"a blank line ends it": {
			"  # Sources: internal/domain/tasks,\n\n  # internal/nope\n", ""},
		"an unlisted top folder": {
			"  # Sources: internal/domain/tasks, scripts/build.sh and their tests.\n",
			"tasks-and-board.feature:3: cited path scripts/build.sh is under scripts/, a top folder rootFolders does not list"},
		"a typo in the top folder": {
			"  # Sources: intenal/tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names intenal/tasks, which doesn't exist"},
		"a top folder in the wrong case": {
			"  # Sources: Internal/tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names Internal/tasks, which doesn't exist"},
		"a bare file name": {
			"  # Sources: internal/domain/tasks, TaskPage.tsx and their tests.\n",
			"tasks-and-board.feature:3: Sources names TaskPage.tsx, which isn't at the repo root; name the path from the repo root"},
		"a plain word": {
			"  # Sources: internal/domain/tasks with ui/src/pages/TaskPage.tsx and their tests.\n",
			`tasks-and-board.feature:3: Sources names with, which isn't a path; a Sources sentence holds only paths and "and their/its tests", ending with a full stop`},
		"a rule ends a sentence with no full stop": {
			"  # Sources: internal/domain/tasks and their tests\n  # ---- The board ----\n", ""},
		"a path from ./": {
			"  # Sources: ./internal/domain/tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names ./internal/domain/tasks; name the path from the repo root, without ./"},
		"a folder below the top in the wrong case": {
			"  # Sources: internal/Tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names internal/Tasks, which doesn't exist"},
		"a folder with a slash at the end": {
			"  # Sources: internal/domain/tasks/ and their tests.\n", ""},
		"only a slash": {
			"  # Sources: internal/domain/tasks, / and their tests.\n",
			"tasks-and-board.feature:3: Sources names /, which isn't a path into the repo; name it from the repo root, with no empty, . or .. part"},
		"a leading slash": {
			"  # Sources: /internal/domain/tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names /internal/domain/tasks, which isn't a path into the repo"},
		"an empty part": {
			"  # Sources: internal//tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names internal//tasks, which isn't a path into the repo"},
		"a . part": {
			"  # Sources: internal/./tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names internal/./tasks, which isn't a path into the repo"},
		"a .. part": {
			"  # Sources: internal/domain/tasks/../tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names internal/domain/tasks/../tasks, which isn't a path into the repo"},
		"a .. part out of an unlisted top folder": {
			"  # Sources: scripts/../internal/domain/tasks and their tests.\n",
			"tasks-and-board.feature:3: Sources names scripts/../internal/domain/tasks, which isn't a path into the repo"},
	} {
		t.Run(name, func(t *testing.T) {
			files := maps.Clone(repo)
			files["functional/tasks-and-board.feature"] = feature(c.sources)
			got := problemsOf(t, files)
			if c.want == "" {
				expectProblems(t, got)
			} else {
				expectProblems(t, got, c.want)
			}
		})
	}
}

// listedWhenTheRuleCameIn puts the fixtures' listed package back in
// noSpecYet for one test: the real list is empty now.
func listedWhenTheRuleCameIn(t *testing.T) {
	was := noSpecYet
	noSpecYet = []string{"internal/extensions/schedules"}
	t.Cleanup(func() { noSpecYet = was })
}

func TestEveryDomainAndExtensionPackageIsNamedBySources(t *testing.T) {
	listedWhenTheRuleCameIn(t)
	repo := map[string]string{
		"../internal/domain/tasks/task.go":                "package tasks\n",
		"../internal/domain/tasks/resume.go":              "package tasks\n",
		"../internal/domain/tasks/taskstest/taskstest.go": "package taskstest\n",
		"../internal/domain/tasks/waitlist/waitlist.go":   "package waitlist\n",
		"../internal/domain/tasks/only/only_test.go":      "package only\n",
		"../internal/domain/tasks/testdata/fixture.go":    "package fixture\n",
		"../internal/extensions/extension.go":             "package extensions\n",
		"../internal/extensions/schedules/schedules.go":   "package schedules\n",
		"../internal/extensions/schedules/schedules.toml": "",
	}
	const list = "# The spec\n\n## Code with no spec yet\n\nWhat no Sources names.\n\n- `internal/extensions/schedules`: one day.\n"
	feature := func(sources string) string {
		return strings.Replace(goodScenario, "  # Sources: internal/web and its tests.\n", "  # Sources: "+sources+" and their tests.\n", 1)
	}
	all := "internal/web, internal/domain/tasks, internal/domain/tasks/waitlist/, internal/extensions"
	for name, c := range map[string]struct {
		sources, readme, proposal string
		want                      []string
	}{
		"every package named or listed": {all, list, "", nil},
		"no README, so no list": {all, "", "", []string{
			"README.md:1: internal/extensions/schedules is named by no feature file's # Sources: line; name it in the Sources of the feature file that specifies it",
			"README.md:1: internal/extensions/schedules is in noSpecYet but not on Code with no spec yet"}},
		"a folder doesn't name its subpackages": {"internal/web, internal/domain/tasks, internal/extensions", list, "", []string{
			"README.md:3: internal/domain/tasks/waitlist is named by no feature file's # Sources: line"}},
		"a file doesn't name its package": {"internal/web, internal/domain/tasks/resume.go, internal/domain/tasks/waitlist, internal/extensions", list, "", []string{
			"README.md:3: internal/domain/tasks is named by no feature file's # Sources: line"}},
		"a proposal's Sources names nothing": {"internal/web, internal/domain/tasks, internal/domain/tasks/waitlist", list, "internal/extensions", []string{
			"README.md:3: internal/extensions is named by no feature file's # Sources: line"}},
		"a listed entry a Sources names": {all + ", internal/extensions/schedules", list, "", []string{
			"spec/functional/tasks-and-board.feature's Sources names it; take it off the list"}},
		"an entry added to the list": {all, list + "- `internal/domain/tasks`\n", "", []string{
			"README.md:8: internal/domain/tasks is on Code with no spec yet, which may only shrink; name it in the Sources of the feature file that specifies it"}},
		"an entry taken off the list but kept in noSpecYet": {all + ", internal/extensions/schedules", "# The spec\n\n## Code with no spec yet\n\nWhat no Sources names.\n", "", []string{
			"README.md:3: internal/extensions/schedules is in noSpecYet but not on Code with no spec yet; drop it from noSpecYet in tools/speccheck/specified.go too"}},
		"an entry under a later heading is no entry": {all, list + "\n## Later\n\n- `internal/domain/gone`\n", "", nil},
	} {
		t.Run(name, func(t *testing.T) {
			files := maps.Clone(repo)
			files["functional/tasks-and-board.feature"] = feature(c.sources)
			if c.readme != "" {
				files["README.md"] = c.readme
			}
			if c.proposal != "" {
				files["proposals/later.feature"] = strings.Replace(goodProposal, "  # Sources: internal/web and its tests.\n", "  # Sources: "+c.proposal+" and its tests.\n", 1)
			}
			expectProblems(t, problemsOf(t, files), c.want...)
		})
	}
}

func TestAListedEntryThatIsNoPackageFails(t *testing.T) {
	listedWhenTheRuleCameIn(t)
	expectProblems(t, problemsOf(t, map[string]string{
		"functional/tasks-and-board.feature": goodScenario,
		"README.md":                          "## Code with no spec yet\n\n- `internal/extensions/schedules`\n",
	}), "README.md:3: internal/extensions/schedules is on Code with no spec yet, and is no package under internal/domain or internal/extensions; take it off the list")
}
