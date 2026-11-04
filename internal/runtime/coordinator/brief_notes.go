package coordinator

import (
	"strings"

	"gitlab.com/dev.chempa/agentry/internal/domain/agents"
	"gitlab.com/dev.chempa/agentry/internal/runtime/machines"
)

// keepYourNotes reminds an agent going on with its session of the notes the
// session's first brief asked it to keep, on this task or a sibling story.
func keepYourNotes(b *strings.Builder) {
	section(b, "Your notes", "Before you finish, update your notes in "+machines.KnowledgeFile+", as the first brief of this session said.")
}

// knowledge is the agent's notes from earlier tasks, and how to keep them.
func knowledge(b *strings.Builder, agent agents.Agent) {
	notes, earlier := notesForBrief(agent.Knowledge)
	if notes == "" && len(earlier) == 0 {
		notes = "(Nothing yet.)"
	}
	if len(earlier) > 0 {
		notes += "\n\nYour notes on earlier tasks are in " + machines.KnowledgeFile + ", not here, to keep this brief short. " +
			"Open a section only when this task needs it (search the file for its heading). The file holds all your notes, " +
			"more than this brief shows: change it in place and never write it out again from this brief.\n\n- " +
			strings.Join(earlier, "\n- ")
	}
	section(b, "What you know from earlier tasks", notes+"\n\n"+
		"These notes are yours, kept across tasks, in "+machines.KnowledgeFile+". Before you finish, update that file: add what "+
		"you learned that will help on a later task (how this codebase does things, a trap you hit, what the user prefers), "+
		"fix what turned out wrong, and drop what no longer matters. Keep it short: a few lines per point. Leave it as it is "+
		"if you learned nothing new.")
}
