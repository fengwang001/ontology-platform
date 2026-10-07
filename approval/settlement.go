package approval

func (s *Service) settleTarget(l *licenseState, targetID string, recordedDay int) {
	affected := map[string]struct{}{targetID: {}}
	for len(affected) > 0 {
		var id string
		for id = range affected {
			break
		}
		delete(affected, id)
		if s.settleOne(l, id, recordedDay) {
			for _, changedID := range s.cascade(l, recordedDay) {
				affected[changedID] = struct{}{}
			}
		}
	}
	l.refreshFinal()
}

func (s *Service) settleAll(l *licenseState, recordedDay int) {
	changed := true
	for changed {
		changed = false
		for _, stage := range l.stages {
			if s.settleOne(l, stage.definition.ID, recordedDay) {
				changed = true
			}
		}
		s.cascade(l, recordedDay)
	}
	l.refreshFinal()
}

func (s *Service) cascade(l *licenseState, recordedDay int) []string {
	changedIDs := []string{}
	for id, stage := range l.stages {
		if stage.status != StageNotStarted {
			continue
		}
		if l.hasFailure() {
			stage.status = StageTerminated
			l.append(Event{At: recordedDay, Kind: eventTerminate, StageID: id, Recorded: recordedDay})
			changedIDs = append(changedIDs, id)
			continue
		}
		startAt, ready := s.readyAt(l, stage.definition)
		if !ready {
			continue
		}
		l.startStage(id, startAt, recordedDay)
		changedIDs = append(changedIDs, id)
	}

	for _, stage := range l.stages {
		if stage.status == StageNotStarted && !s.prerequisitesPassed(l, stage.definition) {
			if s.prerequisitesBlocked(l, stage.definition) {
				stage.status = StageTerminated
				l.append(Event{At: recordedDay, Kind: eventTerminate, StageID: stage.definition.ID, Recorded: recordedDay})
				changedIDs = append(changedIDs, stage.definition.ID)
			}
		}
	}
	return changedIDs
}

func (l *licenseState) hasFailure() bool {
	for _, stage := range l.stages {
		if stage.status == StageFailed {
			return true
		}
	}
	return false
}

func (s *Service) settleOne(l *licenseState, targetID string, recordedDay int) bool {
	stage := l.stages[targetID]
	if stage.status == StageCorrecting && stage.correctionDueAt != nil && recordedDay > *stage.correctionDueAt {
		failedAt := s.days.NextWorkday(*stage.correctionDueAt)
		s.failStage(l, stage, failedAt, recordedDay, true)
		return true
	}
	if stage.status == StageProcessing && stage.definition.AutoPassOnTime {
		due := s.dueAt(stage)
		if due != nil && recordedDay > *due {
			passedAt := s.days.NextWorkday(*due)
			s.passStage(l, stage, passedAt, recordedDay, true)
			return true
		}
	}
	if stage.status == StageProcessing && !stage.definition.AutoPassOnTime {
		due := s.dueAt(stage)
		if due != nil && recordedDay > *due && s.days.IsWorkday(recordedDay) && !stage.overdue {
			stage.overdue = true
			overdueSince := s.days.NextWorkday(*due)
			stage.overdueSince = &overdueSince
		}
	}
	return false
}

func (s *Service) readyAt(l *licenseState, definition StageDefinition) (int, bool) {
	if len(definition.Prerequisites) == 0 {
		return l.acceptedAt, true
	}
	startAt := 0
	for _, prerequisiteID := range definition.Prerequisites {
		prerequisite := l.stages[prerequisiteID]
		if prerequisite.status != StagePassed || prerequisite.passedAt == nil {
			return 0, false
		}
		if *prerequisite.passedAt > startAt {
			startAt = *prerequisite.passedAt
		}
	}
	return startAt, true
}

func (s *Service) prerequisitesPassed(l *licenseState, definition StageDefinition) bool {
	for _, prerequisiteID := range definition.Prerequisites {
		if l.stages[prerequisiteID].status != StagePassed {
			return false
		}
	}
	return true
}

func (s *Service) prerequisitesBlocked(l *licenseState, definition StageDefinition) bool {
	for _, prerequisiteID := range definition.Prerequisites {
		switch l.stages[prerequisiteID].status {
		case StageFailed, StageTerminated:
			return true
		}
	}
	return false
}

func (s *Service) passStage(l *licenseState, stage *stageState, at, recordedDay int, automatic bool) {
	overdue := stage.overdue
	stage.status = StagePassed
	stage.passedAt = &at
	stage.failedAt = nil
	stage.correctionAt = nil
	stage.correctionDueAt = nil
	kind := eventPass
	actor := ""
	if automatic {
		kind = eventAutoPass
	}
	l.append(Event{At: at, Kind: kind, StageID: stage.definition.ID, Actor: actor, Recorded: recordedDay})
	stage.overdue = overdue
}

func (s *Service) failStage(l *licenseState, stage *stageState, at, recordedDay int, automatic bool) {
	stage.status = StageFailed
	stage.failedAt = &at
	stage.passedAt = nil
	stage.correctionAt = nil
	stage.correctionDueAt = nil
	overdue := stage.overdue
	kind := eventFail
	if automatic {
		kind = eventCorrectionExpired
	}
	l.append(Event{At: at, Kind: kind, StageID: stage.definition.ID, Recorded: recordedDay})
	stage.overdue = overdue
}

func (l *licenseState) refreshFinal() {
	allPassed := true
	var latestPass int
	hasFailure := false
	activeAfterFailure := 0
	var lastTerminal int
	for _, stage := range l.stages {
		switch stage.status {
		case StagePassed:
			if stage.passedAt != nil && *stage.passedAt > latestPass {
				latestPass = *stage.passedAt
				if *stage.passedAt > lastTerminal {
					lastTerminal = *stage.passedAt
				}
			}
		case StageFailed:
			hasFailure = true
			if stage.failedAt != nil && *stage.failedAt > lastTerminal {
				lastTerminal = *stage.failedAt
			}
		case StageNotStarted, StageProcessing, StageCorrecting:
			allPassed = false
			activeAfterFailure++
		case StageTerminated:
			allPassed = false
		}
	}
	if hasFailure {
		l.status = OverallDenied
		if activeAfterFailure == 0 {
			l.finalAt = &lastTerminal
		}
		return
	}
	if allPassed {
		l.status = OverallGranted
		l.finalAt = &latestPass
	}
}
