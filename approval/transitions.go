package approval

func (l *licenseState) startStage(stageID string, effectiveDay, recordedDay int) {
	stage := l.stages[stageID]
	if stage.status != StageNotStarted {
		return
	}
	stage.status = StageProcessing
	stage.startedAt = intPtr(effectiveDay)
	stage.remainingLimit = stage.definition.TimeLimit
	stage.segmentStartAt = intPtr(effectiveDay)
	l.append(Event{At: effectiveDay, Kind: eventStart, StageID: stageID, Recorded: recordedDay})
}

func (l *licenseState) append(event Event) {
	event.Sequence = len(l.events)
	l.events = append(l.events, event)
}

func intPtr(value int) *int {
	return &value
}

func boolPayload(value bool) int {
	if value {
		return 1
	}
	return 0
}
