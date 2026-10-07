package approval

func (s *Service) Pass(cmd StageCommand) error {
	state, stage, err := s.validateStageCommand(cmd)
	if err != nil {
		return err
	}
	defer state.mu.Unlock()
	if state.finalAt != nil {
		return ErrInvalidState
	}
	if stage.status != StageProcessing {
		return ErrInvalidState
	}
	if err := s.permissionErrorWithProjection(state, stage, cmd.Actor, cmd.Day, StageProcessing); err != nil {
		return err
	}
	if !s.prerequisitesPassed(state, stage.definition) {
		return ErrPrerequisiteNotPassed
	}
	s.commitDay(cmd.Day)
	s.settleTarget(state, stage.definition.ID, cmd.Day)
	if state.finalAt != nil {
		return ErrInvalidState
	}
	if stage.status != StageProcessing {
		return ErrInvalidState
	}
	s.passStage(state, stage, cmd.Day, cmd.Day, false)
	l := state
	l.events[len(l.events)-1].Actor = cmd.Actor
	l.events[len(l.events)-1].Payload = boolPayload(stage.overdue)
	s.settleTarget(state, stage.definition.ID, cmd.Day)
	return nil
}

func (s *Service) Fail(cmd StageCommand) error {
	state, stage, err := s.validateStageCommand(cmd)
	if err != nil {
		return err
	}
	defer state.mu.Unlock()
	if state.finalAt != nil {
		return ErrInvalidState
	}
	if stage.status != StageProcessing {
		return ErrInvalidState
	}
	if err := s.permissionErrorWithProjection(state, stage, cmd.Actor, cmd.Day, StageProcessing); err != nil {
		return err
	}
	if !s.prerequisitesPassed(state, stage.definition) {
		return ErrPrerequisiteNotPassed
	}
	s.commitDay(cmd.Day)
	s.settleTarget(state, stage.definition.ID, cmd.Day)
	if state.finalAt != nil {
		return ErrInvalidState
	}
	if stage.status != StageProcessing {
		return ErrInvalidState
	}
	s.failStage(state, stage, cmd.Day, cmd.Day, false)
	state.events[len(state.events)-1].Actor = cmd.Actor
	state.events[len(state.events)-1].Payload = boolPayload(stage.overdue)
	s.settleTarget(state, stage.definition.ID, cmd.Day)
	return nil
}

func (s *Service) RequestCorrection(cmd StageCommand) error {
	state, stage, err := s.validateStageCommand(cmd)
	if err != nil {
		return err
	}
	defer state.mu.Unlock()
	if state.finalAt != nil {
		return ErrInvalidState
	}
	if stage.status != StageProcessing {
		return ErrInvalidState
	}
	if err := s.permissionErrorWithProjection(state, stage, cmd.Actor, cmd.Day, StageProcessing); err != nil {
		return err
	}
	if !s.prerequisitesPassed(state, stage.definition) {
		return ErrPrerequisiteNotPassed
	}
	if stage.correctionCount >= stage.definition.CorrectionLimit {
		return ErrCorrectionLimit
	}
	s.commitDay(cmd.Day)
	s.settleTarget(state, stage.definition.ID, cmd.Day)
	if state.finalAt != nil || stage.status != StageProcessing {
		return ErrInvalidState
	}
	if stage.correctionCount >= stage.definition.CorrectionLimit {
		return ErrCorrectionLimit
	}
	used := s.elapsedWorkdays(stage, cmd.Day)
	stage.remainingLimit -= used
	if stage.remainingLimit < 0 {
		stage.remainingLimit = 0
	}
	stage.correctionCount++
	stage.status = StageCorrecting
	stage.correctionAt = intPtr(cmd.Day)
	deadline := s.correctionDeadline(stage, cmd.Day)
	stage.correctionDueAt = &deadline
	stage.segmentStartAt = nil
	state.append(Event{At: cmd.Day, Kind: eventRequestCorrection, StageID: stage.definition.ID, Actor: cmd.Actor, Payload: deadline, Recorded: cmd.Day})
	return nil
}

func (s *Service) SubmitCorrection(cmd StageCommand) error {
	state, stage, err := s.validateStageCommand(cmd)
	if err != nil {
		return err
	}
	defer state.mu.Unlock()
	if state.finalAt != nil {
		return ErrInvalidState
	}
	if stage.status != StageCorrecting {
		return ErrInvalidState
	}
	if err := s.permissionErrorWithProjection(state, stage, cmd.Actor, cmd.Day, StageCorrecting); err != nil {
		return err
	}
	if !s.prerequisitesPassed(state, stage.definition) {
		return ErrPrerequisiteNotPassed
	}
	s.commitDay(cmd.Day)
	s.settleTarget(state, stage.definition.ID, cmd.Day)
	if state.finalAt != nil || stage.status != StageCorrecting {
		return ErrInvalidState
	}
	if stage.correctionDueAt == nil || cmd.Day > *stage.correctionDueAt {
		return ErrInvalidState
	}
	stage.status = StageProcessing
	stage.correctionAt = nil
	stage.correctionDueAt = nil
	stage.resumeAt = intPtr(cmd.Day)
	stage.segmentStartAt = intPtr(cmd.Day)
	state.append(Event{At: cmd.Day, Kind: eventSubmitCorrection, StageID: stage.definition.ID, Actor: cmd.Actor, Recorded: cmd.Day})
	s.settleTarget(state, stage.definition.ID, cmd.Day)
	return nil
}

func (s *Service) Withdraw(req WithdrawRequest) error {
	if req.LicenseID == "" || req.Actor == "" || req.Day <= 0 {
		return ErrInvalidArgument
	}
	state, ok := s.lookupLicense(req.LicenseID)
	if !ok {
		return ErrNotFound
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	current := s.clock.Load()
	clockBack := current != nil && req.Day < *current
	if clockBack {
		return ErrClockMovedBack
	}
	if req.Actor != state.applicant {
		projection, err := s.project(state.typ, state.acceptedAt, append([]Event(nil), state.events...), req.Day)
		if err != nil {
			return err
		}
		if projection.finalAt != nil || projection.status == OverallWithdrawn {
			return ErrInvalidState
		}
		return ErrForbidden
	}
	s.commitDay(req.Day)
	s.settleAll(state, req.Day)
	if state.finalAt != nil || state.status == OverallWithdrawn {
		return ErrInvalidState
	}
	state.status = OverallWithdrawn
	state.finalAt = intPtr(req.Day)
	for _, stage := range state.stages {
		if stage.status == StageNotStarted || stage.status == StageProcessing || stage.status == StageCorrecting {
			stage.status = StageWithdrawn
		}
	}
	state.append(Event{At: req.Day, Kind: eventWithdraw, Actor: req.Actor, Recorded: req.Day})
	return nil
}
