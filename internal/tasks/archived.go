package tasks

import (
	"encoding/json"

	"gitlab.com/dev.chempa/agentry/internal/events"
)

// Archived is the summary an archived task keeps: the "summary" of its
// stream's last task.archived that no task.unarchived follows. It is false
// for a task that isn't archived. A marker whose summary can't be read
// panics, as an event apply can't take does.
func Archived(stream []events.Event) (Summary, bool) {
	var marker *events.Event
	for i, e := range stream {
		switch e.Type {
		case events.TaskArchived:
			marker = &stream[i]
		case events.TaskUnarchived:
			marker = nil
		}
	}
	if marker == nil {
		return Summary{}, false
	}
	return summaryIn(*marker), true
}

// summaryIn reads a marker's summary, as the archiver wrote it from
// SummaryOf. Its id is the stream's, whatever the summary says.
func summaryIn(marker events.Event) Summary {
	data, err := json.Marshal(marker.Data["summary"])
	if err != nil {
		panic(err)
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		panic("the marker's summary: " + err.Error())
	}
	summary.ID = marker.Stream
	return summary
}
