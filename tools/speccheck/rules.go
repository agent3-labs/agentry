package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// functionalPrefixes and nonFunctionalPrefixes give each feature file, by
// its base name, the prefix of its scenario IDs: the files under
// functional/, then those under non-functional/. prefixes holds both; a
// feature file with any other name is a problem, outside proposals/.
var functionalPrefixes = map[string]string{
	"tasks-and-board.feature":          "TASK",
	"epics-and-stories.feature":        "EPIC",
	"approvals-and-questions.feature":  "APPROVE",
	"ideas.feature":                    "IDEA",
	"schedules.feature":                "SCHED",
	"teams-and-templates.feature":      "TEAM",
	"policy.feature":                   "POLICY",
	"sessions.feature":                 "SESSION",
	"coordinator-and-handoffs.feature": "HANDOFF",
	"agents.feature":                   "TURN",
	"worktrees-and-branches.feature":   "TREE",
	"workers.feature":                  "MACHINE",
	"shared-files.feature":             "FILES",
	"sign-in.feature":                  "SIGNIN",
	"notifications.feature":            "NOTIFY",
	"settings.feature":                 "SET",
	"claude-code-tools.feature":        "TOOLS",
	"demo.feature":                     "DEMO",
}

var nonFunctionalPrefixes = map[string]string{
	"reliability.feature":   "REL",
	"security.feature":      "SEC",
	"operations.feature":    "OPS",
	"performance.feature":   "PERF",
	"limits.feature":        "LIM",
	"usability.feature":     "USE",
	"compatibility.feature": "COMPAT",
	"observability.feature": "OBS",
}

var prefixes = joined(functionalPrefixes, nonFunctionalPrefixes)

// proposalPrefix is the prefix of every scenario ID under proposals/,
// whatever the file's name.
const proposalPrefix = "PROP"

// targetTagStart starts a proposal's target tag, @to-policy or
// @to-new-output-gate: the feature file its scenario moves to when built.
const targetTagStart = "@to-"

// newStem is the stem of a target file that does not exist yet: new- and
// lower-case words joined by dashes.
var newStem = regexp.MustCompile(`^new-[a-z]+(-[a-z]+)*$`)

// joined is one table holding every entry of the tables.
func joined(tables ...map[string]string) map[string]string {
	all := map[string]string{}
	for _, table := range tables {
		maps.Copy(all, table)
	}
	return all
}

// The check tags: every scenario carries exactly one.
const (
	tagTest      = "@test"
	tagBenchmark = "@benchmark"
	tagManual    = "@manual"
)

var checkTags = []string{tagTest, tagBenchmark, tagManual}

// roleTags name whose point of view a functional scenario takes: every
// scenario under functional/ carries exactly one.
var roleTags = []string{"@user", "@coordinator", "@agent", "@claude-code", "@worker"}

// outcomeWords are the words that make a Then a yes/no outcome when it has
// no number. Matched as whole words, case-insensitive; a contraction such
// as "doesn't" is one word and matches nothing. This is the one list;
// spec/README.md rule 4 repeats it word for word.
var outcomeWords = []string{
	"not", "no", "never", "nothing", "cannot", "yes", "exactly", "every", "each", "once",
	"only", "same", "empty", "refused", "denied", "allowed", "asked", "shown", "hidden", "kept",
}

// bannedWords say nothing a checker can judge, so no scenario may use them.
var bannedWords = []string{"quickly", "properly", "appropriately"}

const emDash = "—"

// idOf reads an ID tag such as @TASK-01 into its prefix. ok is false for
// any other tag: no dash, a prefix that is not upper-case letters, or fewer
// than two digits.
func idOf(tagName string) (prefix string, ok bool) {
	prefix, digits, found := strings.Cut(strings.TrimPrefix(tagName, "@"), "-")
	if !found || prefix == "" || len(digits) < 2 {
		return "", false
	}
	for _, r := range prefix {
		if r < 'A' || r > 'Z' {
			return "", false
		}
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return prefix, true
}

// idTags are the scenario's tags that look like IDs, whatever their prefix.
func idTags(s scenario) []tag {
	var ids []tag
	for _, t := range s.tags {
		if _, ok := idOf(t.name); ok {
			ids = append(ids, t)
		}
	}
	return ids
}

// idProblem says what is wrong with the scenario's ID tags under its file's
// prefix, or "" when exactly one ID tag carries that prefix.
func idProblem(s scenario, prefix string) string {
	ids := idTags(s)
	switch {
	case len(ids) == 0:
		return fmt.Sprintf("no ID tag; this file's scenarios are tagged @%s-nn", prefix)
	case len(ids) > 1:
		return fmt.Sprintf("%d ID tags; a scenario has one", len(ids))
	}
	if got, _ := idOf(ids[0].name); got != prefix {
		return fmt.Sprintf("ID %s does not match this file's prefix @%s-nn", ids[0].name, prefix)
	}
	return ""
}

// tagHint adds, to a missing-tag message, why the tags were missed: they
// sit above a blank or comment line instead of directly above the Scenario
// line.
func tagHint(s scenario) string {
	if s.tagsAbove {
		return "; the tags must be on the line directly above Scenario:"
	}
	return ""
}

// checkTagProblem says what is wrong with the scenario's check tags, or ""
// when it has exactly one of them.
func checkTagProblem(s scenario) string {
	var found []string
	for _, t := range s.tags {
		for _, check := range checkTags {
			if t.name == check {
				found = append(found, t.name)
			}
		}
	}
	switch len(found) {
	case 1:
		return ""
	case 0:
		return "no check tag; a scenario has exactly one of " + strings.Join(checkTags, ", ")
	}
	return "more than one check tag (" + strings.Join(found, ", ") + "); a scenario has exactly one"
}

// roleTagProblem says what is wrong with a functional scenario's role
// tags, or "" when it has exactly one of them.
func roleTagProblem(s scenario) string {
	found := roleTagsOf(s)
	switch len(found) {
	case 1:
		return ""
	case 0:
		return "no role tag; a functional scenario has exactly one of " + strings.Join(roleTags, ", ")
	}
	return "more than one role tag (" + strings.Join(found, ", ") + "); a functional scenario has exactly one"
}

func roleTagsOf(s scenario) []string {
	var found []string
	for _, t := range s.tags {
		if slices.Contains(roleTags, t.name) {
			found = append(found, t.name)
		}
	}
	return found
}

// targetProblem says what is wrong with a proposal's target tags, or ""
// when it has exactly one and it names a feature file in prefixes or a new
// one. target is that tag's stem, such as policy or new-output-gate, or ""
// when there is a problem.
func targetProblem(s scenario) (target, problem string) {
	var found []string
	for _, t := range s.tags {
		if strings.HasPrefix(t.name, targetTagStart) {
			found = append(found, t.name)
		}
	}
	switch {
	case len(found) == 0:
		return "", "no target tag; a proposal has exactly one " + targetTagStart + "<stem>, naming the file it moves to when built: " + targetHint()
	case len(found) > 1:
		return "", "more than one target tag (" + strings.Join(found, ", ") + "); a proposal has exactly one"
	}
	stem := strings.TrimPrefix(found[0], targetTagStart)
	if _, known := prefixes[stem+".feature"]; !known && !newStem.MatchString(stem) {
		return "", "unknown target " + found[0] + "; " + targetHint()
	}
	return stem, ""
}

// strayTargetProblem says what is wrong with a target tag on a scenario
// in no file directly under proposals/, or "" when it has none: a built
// scenario drops it.
func strayTargetProblem(s scenario) string {
	for _, t := range s.tags {
		if strings.HasPrefix(t.name, targetTagStart) {
			return "target tag " + t.name + " belongs only in a file directly under spec/proposals; a built scenario drops it"
		}
	}
	return ""
}

func targetHint() string {
	return "the known stems are " + knownStems() + ", or new-<stem> in lower-case words joined by dashes for a file that does not exist yet"
}

// proposalRoleProblem applies the role tag rule of a proposal's target: a
// proposal aimed at a functional file needs exactly one role tag, as a
// scenario in that file would; one aimed at a non-functional file carries
// none, as the scenarios there carry none; one aimed at a new file may carry
// one or none, since whether that file is functional is decided when it is
// made.
func proposalRoleProblem(s scenario, target string) string {
	if _, functional := functionalPrefixes[target+".feature"]; functional {
		if message := roleTagProblem(s); message != "" {
			return message + " (" + targetTagStart + target + " is a functional file)"
		}
		return ""
	}
	found := roleTagsOf(s)
	if _, nonFunctional := nonFunctionalPrefixes[target+".feature"]; nonFunctional && len(found) > 0 {
		noun := "role tag"
		if len(found) > 1 {
			noun = "role tags"
		}
		return noun + " " + strings.Join(found, ", ") + " on a proposal for a non-functional file; a non-functional scenario carries none (" + targetTagStart + target + " is a non-functional file)"
	}
	if strings.HasPrefix(target, "new-") && len(found) > 1 {
		return "more than one role tag (" + strings.Join(found, ", ") + "); a proposal for a new file has one or none"
	}
	return ""
}

// hasTag is true when the scenario carries the tag.
func hasTag(s scenario, name string) bool {
	for _, t := range s.tags {
		if t.name == name {
			return true
		}
	}
	return false
}

// hasOutcome is true when a Then line states a number or a yes/no outcome.
func hasOutcome(text string) bool {
	if strings.ContainsFunc(text, unicode.IsDigit) {
		return true
	}
	for _, word := range words(text) {
		for _, outcome := range outcomeWords {
			if word == outcome {
				return true
			}
		}
	}
	return false
}

// bannedWordIn names the first banned word used in the text, or "".
func bannedWordIn(text string) string {
	for _, word := range words(text) {
		for _, banned := range bannedWords {
			if word == banned {
				return banned
			}
		}
	}
	return ""
}

// words splits a line into lower-case words. Letters and apostrophes make
// a word, so "doesn't" is one word, not "doesn" and "t".
func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	})
}

// lastIDBeforeLineRule is each prefix's highest ID when the rule against
// line references came in. A scenario numbered above its prefix's entry
// may not cite "line N" or "lines N", in any case, or a path such as
// x.go:42, in a comment: lines move as dev moves, so it names a function
// or a test instead. The scenarios at or below the entry are left as they
// are. The functional prefixes came in when the files were regrouped by feature, which
// renumbered the scenarios that were there before, so each entry is its
// file's highest ID then.
var lastIDBeforeLineRule = map[string]int{
	"TASK": 55, "EPIC": 19, "APPROVE": 7, "IDEA": 17, "TEAM": 14, "POLICY": 46,
	"SESSION": 5, "HANDOFF": 42, "TURN": 19, "TREE": 12, "MACHINE": 34, "FILES": 8,
	"SIGNIN": 7, "NOTIFY": 3, "SET": 4, "TOOLS": 54, "SCHED": 0,
	"REL": 54, "SEC": 68, "OPS": 42, "PERF": 16, "LIM": 35,
	"USE": 29, "COMPAT": 27, "OBS": 34, "PROP": 0,
}

var lineReference = regexp.MustCompile(`(?i)\blines? [0-9]`)

// lineReferences finds the comment lines of a scenario that cite a line
// number, when the scenario is newer than the rule against them. A step may
// say "line 1 of the report": that is what Agentry does, not a citation.
func lineReferences(s scenario, prefix string) []line {
	if !newerThanLineRule(s, prefix) {
		return nil
	}
	var found []line
	for _, l := range s.body {
		if isComment(l.text) && (lineReference.MatchString(l.text) || citesLineOfPath(l.text)) {
			found = append(found, l)
		}
	}
	return found
}

// newerThanLineRule is true when the scenario's one ID with its file's
// prefix is numbered above that prefix's entry in lastIDBeforeLineRule.
func newerThanLineRule(s scenario, prefix string) bool {
	last, known := lastIDBeforeLineRule[prefix]
	if !known || idProblem(s, prefix) != "" {
		return false
	}
	_, digits, _ := strings.Cut(idTags(s)[0].name, "-")
	number, err := strconv.Atoi(digits)
	return err == nil && number > last
}
