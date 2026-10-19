package current

import "gitlab.com/dev.chempa/agentry/internal/tasks"

// ArchiveInPlace keeps a task as its summary alone, as its marker would,
// with no marker in the log and the position where it was: for the
// snapshot benchmark, which times a state of the working set alone against
// the log the state was built from.
func (s *State) ArchiveInPlace(summary tasks.Summary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, summary.ID)
	s.archived[summary.ID] = summary
}
