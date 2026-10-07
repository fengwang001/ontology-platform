package approval

func (s *Service) QueryProgress(licenseID string, day int) (Progress, error) {
	if licenseID == "" || day <= 0 {
		return Progress{}, ErrInvalidArgument
	}
	state, ok := s.lookupLicense(licenseID)
	if !ok {
		return Progress{}, ErrNotFound
	}
	state.mu.Lock()
	events := append([]Event(nil), state.events...)
	typ := state.typ
	acceptedAt := state.acceptedAt
	state.mu.Unlock()

	projection, err := s.project(typ, acceptedAt, events, day)
	if err != nil {
		return Progress{}, err
	}
	progress := Progress{
		LicenseID: licenseID,
		Status:    projection.status,
		FinalAt:   projection.finalAt,
		Stages:    map[string]StageView{},
	}
	for id, stage := range projection.stages {
		progress.Stages[id] = s.stageView(stage, day)
	}
	return progress, nil
}

func (s *Service) QueryStage(licenseID, stageID string, day int) (StageView, error) {
	if licenseID == "" || stageID == "" || day <= 0 {
		return StageView{}, ErrInvalidArgument
	}
	state, ok := s.lookupLicense(licenseID)
	if !ok {
		return StageView{}, ErrNotFound
	}
	state.mu.Lock()
	events := append([]Event(nil), state.events...)
	typ := state.typ
	acceptedAt := state.acceptedAt
	state.mu.Unlock()
	if _, exists := stageDefinition(typ, stageID); !exists {
		return StageView{}, ErrNotFound
	}
	projection, err := s.project(typ, acceptedAt, events, day)
	if err != nil {
		return StageView{}, err
	}
	stage, exists := projection.stages[stageID]
	if !exists {
		return StageView{}, ErrNotFound
	}
	return s.stageView(stage, day), nil
}

func stageDefinition(typ LicenseType, stageID string) (StageDefinition, bool) {
	for _, stage := range typ.Stages {
		if stage.ID == stageID {
			return stage, true
		}
	}
	return StageDefinition{}, false
}

func (s *Service) stageView(stage *stageState, day int) StageView {
	view := StageView{
		ID:                 stage.definition.ID,
		Department:         stage.definition.Department,
		Status:             stage.status,
		RemainingWorkdays:  s.remainingWorkdays(stage, day),
		Overdue:            stage.overdue,
		StartedAt:          stage.startedAt,
		PassedAt:           stage.passedAt,
		FailedAt:           stage.failedAt,
		CorrectionDeadline: stage.correctionDueAt,
	}
	if stage.status == StageProcessing && !stage.definition.AutoPassOnTime {
		due := s.dueAt(stage)
		if due != nil && day > *due && s.days.IsWorkday(day) {
			view.Overdue = true
		}
	}
	return view
}
