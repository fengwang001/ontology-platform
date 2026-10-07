package approval

import "sort"

type projectionState struct {
	status  string
	finalAt *int
	stages  map[string]*stageState
}

func (s *Service) project(typ LicenseType, acceptedAt int, events []Event, day int) (*projectionState, error) {
	state := &projectionState{
		status: OverallAccepted,
		stages: map[string]*stageState{},
	}
	for _, definition := range typ.Stages {
		state.stages[definition.ID] = &stageState{
			definition:     definition,
			status:         StageNotStarted,
			remainingLimit: definition.TimeLimit,
		}
	}

	visible := make([]Event, 0, len(events))
	for _, event := range events {
		if event.At <= day && event.Recorded <= day {
			visible = append(visible, event)
		}
	}
	sort.SliceStable(visible, func(i, j int) bool {
		if visible[i].Recorded != visible[j].Recorded {
			return visible[i].Recorded < visible[j].Recorded
		}
		return visible[i].Sequence < visible[j].Sequence
	})

	for _, event := range visible {
		switch event.Kind {
		case eventStart:
			stage := state.stages[event.StageID]
			stage.status = StageProcessing
			stage.startedAt = &event.At
			stage.segmentStartAt = &event.At
			stage.remainingLimit = stage.definition.TimeLimit
			state.status = OverallProcessing
		case eventPass, eventAutoPass:
			stage := state.stages[event.StageID]
			stage.overdue = event.Payload == 1
			stage.status = StagePassed
			stage.passedAt = &event.At
			stage.correctionAt = nil
			stage.correctionDueAt = nil
			stage.segmentStartAt = nil
		case eventFail, eventCorrectionExpired:
			stage := state.stages[event.StageID]
			stage.overdue = event.Payload == 1
			stage.status = StageFailed
			stage.failedAt = &event.At
			stage.correctionAt = nil
			stage.correctionDueAt = nil
			stage.segmentStartAt = nil
		case eventRequestCorrection:
			stage := state.stages[event.StageID]
			used := s.elapsedWorkdays(stage, event.At)
			stage.remainingLimit -= used
			if stage.remainingLimit < 0 {
				stage.remainingLimit = 0
			}
			stage.status = StageCorrecting
			stage.correctionAt = &event.At
			deadline := event.Payload
			stage.correctionDueAt = &deadline
			stage.segmentStartAt = nil
		case eventSubmitCorrection:
			stage := state.stages[event.StageID]
			stage.status = StageProcessing
			stage.correctionAt = nil
			stage.correctionDueAt = nil
			stage.resumeAt = &event.At
			stage.segmentStartAt = &event.At
		case eventTerminate:
			state.stages[event.StageID].status = StageTerminated
		case eventWithdraw:
			state.status = OverallWithdrawn
			state.finalAt = &event.At
			for _, stage := range state.stages {
				if stage.status == StageNotStarted || stage.status == StageProcessing || stage.status == StageCorrecting {
					stage.status = StageWithdrawn
				}
			}
		}
	}
	s.projectSettlement(typ, state, acceptedAt, day)

	state.status = s.projectedOverall(acceptedAt, state, day)
	return state, nil
}

func (s *Service) projectSettlement(typ LicenseType, state *projectionState, acceptedAt, day int) {
	changed := true
	for changed {
		changed = false
		for _, definition := range typ.Stages {
			stage := state.stages[definition.ID]
			if stage.status == StageCorrecting && stage.correctionDueAt != nil && day > *stage.correctionDueAt {
				failedAt := s.days.NextWorkday(*stage.correctionDueAt)
				if failedAt <= day {
					stage.status = StageFailed
					stage.failedAt = &failedAt
					stage.correctionAt = nil
					stage.correctionDueAt = nil
					stage.segmentStartAt = nil
					changed = true
				}
			}
			if stage.status == StageProcessing && definition.AutoPassOnTime {
				due := s.dueAt(stage)
				if due != nil {
					passedAt := s.days.NextWorkday(*due)
					if passedAt <= day && day >= passedAt {
						stage.status = StagePassed
						stage.passedAt = &passedAt
						stage.correctionAt = nil
						stage.correctionDueAt = nil
						stage.segmentStartAt = nil
						changed = true
					}
				}
			}
		}

		for _, definition := range typ.Stages {
			stage := state.stages[definition.ID]
			if stage.status != StageNotStarted {
				continue
			}
			if s.projectedHasFailure(state) {
				stage.status = StageTerminated
				changed = true
				continue
			}
			startAt, ready := s.projectedReadyAt(acceptedAt, state, definition)
			if ready && startAt <= day {
				stage.status = StageProcessing
				stage.startedAt = &startAt
				stage.segmentStartAt = &startAt
				stage.remainingLimit = definition.TimeLimit
				changed = true
				continue
			}
			if !ready && s.projectedBlocked(state, definition) {
				stage.status = StageTerminated
				changed = true
			}
		}
	}
}

func (s *Service) projectedReadyAt(acceptedAt int, state *projectionState, definition StageDefinition) (int, bool) {
	if len(definition.Prerequisites) == 0 {
		return acceptedAt, true
	}
	startAt := 0
	for _, prerequisiteID := range definition.Prerequisites {
		prerequisite := state.stages[prerequisiteID]
		if prerequisite.status != StagePassed || prerequisite.passedAt == nil {
			return 0, false
		}
		if *prerequisite.passedAt > startAt {
			startAt = *prerequisite.passedAt
		}
	}
	return startAt, true
}

func (s *Service) projectedBlocked(state *projectionState, definition StageDefinition) bool {
	for _, prerequisiteID := range definition.Prerequisites {
		switch state.stages[prerequisiteID].status {
		case StageFailed, StageTerminated:
			return true
		}
	}
	return false
}

func (s *Service) projectedHasFailure(state *projectionState) bool {
	for _, stage := range state.stages {
		if stage.status == StageFailed {
			return true
		}
	}
	return false
}

func (s *Service) projectedOverall(acceptedAt int, state *projectionState, day int) string {
	if day < acceptedAt {
		return OverallAccepted
	}
	if state.status == OverallWithdrawn || state.status == OverallDenied {
		return state.status
	}
	allPassed := true
	var latest int
	active := 0
	hasFailure := false
	var lastTerminal int
	for _, stage := range state.stages {
		if stage.status == StagePassed {
			if stage.passedAt != nil && *stage.passedAt > latest {
				latest = *stage.passedAt
				if *stage.passedAt > lastTerminal {
					lastTerminal = *stage.passedAt
				}
			}
			continue
		}
		if stage.status == StageFailed {
			hasFailure = true
			if stage.failedAt != nil && *stage.failedAt > lastTerminal {
				lastTerminal = *stage.failedAt
			}
		}
		if stage.status == StageProcessing || stage.status == StageCorrecting {
			active++
		}
		allPassed = false
	}
	if allPassed {
		state.finalAt = &latest
		return OverallGranted
	}
	if hasFailure && active == 0 {
		state.finalAt = &lastTerminal
		return OverallDenied
	}
	return OverallProcessing
}
