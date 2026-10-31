package coordinator

import (
	"fmt"
	"regexp"
	"strings"
)

// taskHeading is a notes section about one earlier task or epic, such as
// "## task-46 (Health board build)". Agents file most of their notes that way.
// Only a heading that starts with the task counts: "## Vesper repo (task-80
// epic)" is about the repo, and every later task there needs it.
var taskHeading = regexp.MustCompile(`(?i)^## (task|epic)-\d+\b`)

// notesForBrief is what of an agent's notes goes into a brief: the general
// notes whole, and of the sections about earlier tasks only their headings,
// with their size. Those sections stay in the knowledge file, for the agent to
// open when the task at hand needs one. Every step of a turn reads the whole
// brief again, so notes about unrelated tasks are paid for on every step.
// Notes with no such section come back as they are.
func notesForBrief(notes string) (general string, earlier []string) {
	var keep strings.Builder
	var heading string
	var body strings.Builder
	fenced := false
	flush := func() {
		if heading == "" {
			return
		}
		if taskHeading.MatchString(heading) {
			earlier = append(earlier, fmt.Sprintf("%s (%s)", strings.TrimSpace(strings.TrimPrefix(heading, "## ")), size(body.Len())))
		} else {
			keep.WriteString(heading + "\n" + body.String())
		}
		heading = ""
		body.Reset()
	}
	for _, line := range strings.SplitAfter(notes, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		}
		if !fenced && strings.HasPrefix(line, "## ") {
			flush()
			heading = strings.TrimRight(line, "\n")
			continue
		}
		if heading == "" {
			keep.WriteString(line)
		} else {
			body.WriteString(line)
		}
	}
	flush()
	return strings.TrimSpace(keep.String()), earlier
}

// size says how big a notes section is, so an agent can tell a line from an essay.
func size(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d bytes", bytes)
	}
	return fmt.Sprintf("%d KB", (bytes+512)/1024)
}
