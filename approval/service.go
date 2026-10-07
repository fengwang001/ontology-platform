package approval

import (
	"sync"
	"sync/atomic"
)

type Service struct {
	mu       sync.RWMutex
	days     Calendar
	types    map[string]LicenseType
	licenses map[string]*licenseState
	clock    atomic.Pointer[int]
}

type licenseState struct {
	mu         sync.Mutex
	id         string
	typ        LicenseType
	applicant  string
	actors     map[string]string
	acceptedAt int
	status     string
	finalAt    *int
	stages     map[string]*stageState
	events     []Event
}

type stageState struct {
	definition      StageDefinition
	status          string
	startedAt       *int
	passedAt        *int
	failedAt        *int
	correctionAt    *int
	correctionDueAt *int
	resumeAt        *int
	segmentStartAt  *int
	remainingLimit  int
	correctionCount int
	overdue         bool
	overdueSince    *int
}

func NewService(calendar Calendar, types []LicenseType) (*Service, error) {
	service := &Service{
		days:     calendar,
		types:    make(map[string]LicenseType, len(types)),
		licenses: map[string]*licenseState{},
	}
	for _, spec := range types {
		if err := validateLicenseType(spec); err != nil {
			return nil, err
		}
		if _, exists := service.types[spec.ID]; exists {
			return nil, ErrInvalidArgument
		}
		service.types[spec.ID] = spec
	}
	return service, nil
}

func (s *Service) Accept(req AcceptRequest) error {
	if req.LicenseID == "" || req.TypeID == "" || req.ApplicantID == "" || req.Day <= 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.clock.Load(); current != nil && req.Day < *current {
		return ErrClockMovedBack
	}
	spec, ok := s.types[req.TypeID]
	if !ok {
		return ErrNotFound
	}
	if _, exists := s.licenses[req.LicenseID]; exists {
		return ErrInvalidArgument
	}
	if req.Actors == nil {
		return ErrInvalidArgument
	}
	seenActors := map[string]struct{}{}
	for id, actor := range req.Actors {
		if id == "" || actor.ID != id || actor.Department == "" {
			return ErrInvalidArgument
		}
		if _, duplicate := seenActors[id]; duplicate {
			return ErrInvalidArgument
		}
		seenActors[id] = struct{}{}
	}
	for _, stage := range spec.Stages {
		if _, exists := actorForDepartment(req.Actors, stage.Department); !exists {
			return ErrInvalidArgument
		}
	}

	state := newLicenseState(req, spec)
	s.licenses[req.LicenseID] = state
	s.advanceClockLocked(&req.Day)
	return nil
}

func newLicenseState(req AcceptRequest, spec LicenseType) *licenseState {
	state := &licenseState{
		id:         req.LicenseID,
		typ:        spec,
		applicant:  req.ApplicantID,
		actors:     make(map[string]string, len(req.Actors)),
		acceptedAt: req.Day,
		status:     OverallProcessing,
		stages:     make(map[string]*stageState, len(spec.Stages)),
	}
	for id, actor := range req.Actors {
		state.actors[id] = actor.Department
	}
	for _, definition := range spec.Stages {
		state.stages[definition.ID] = &stageState{
			definition:     definition,
			status:         StageNotStarted,
			remainingLimit: definition.TimeLimit,
		}
	}
	for _, definition := range spec.Stages {
		if len(definition.Prerequisites) == 0 {
			state.startStage(definition.ID, req.Day, req.Day)
		}
	}
	return state
}

func actorForDepartment(actors map[string]Actor, department string) (string, bool) {
	for id, actor := range actors {
		if actor.Department == department {
			return id, true
		}
	}
	return "", false
}

func (s *Service) advanceClockLocked(day *int) {
	if current := s.clock.Load(); current == nil || *day > *current {
		s.clock.Store(day)
	}
}

func (s *Service) lookupLicense(licenseID string) (*licenseState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.licenses[licenseID]
	return state, ok
}

func (s *Service) validateStageCommand(cmd StageCommand) (*licenseState, *stageState, error) {
	if cmd.LicenseID == "" || cmd.StageID == "" || cmd.Actor == "" || cmd.Day <= 0 {
		return nil, nil, ErrInvalidArgument
	}
	current := s.clock.Load()
	if current != nil && cmd.Day < *current {
		return nil, nil, ErrClockMovedBack
	}
	state, ok := s.lookupLicense(cmd.LicenseID)
	if !ok {
		return nil, nil, ErrNotFound
	}
	state.mu.Lock()
	stage, exists := state.stages[cmd.StageID]
	if !exists {
		state.mu.Unlock()
		return nil, nil, ErrNotFound
	}
	return state, stage, nil
}

func validateStagePermission(state *licenseState, stage *stageState, actor string) error {
	department, authorized := state.actors[actor]
	if !authorized || department != stage.definition.Department {
		return ErrForbidden
	}
	return nil
}

func (s *Service) permissionErrorWithProjection(state *licenseState, stage *stageState, actor string, day int, expectedStatus string) error {
	if err := validateStagePermission(state, stage, actor); err == nil {
		return nil
	}
	events := append([]Event(nil), state.events...)
	projection, projectionErr := s.project(state.typ, state.acceptedAt, events, day)
	if projectionErr == nil {
		if projection.finalAt != nil || projection.stages[stage.definition.ID].status != expectedStatus {
			return ErrInvalidState
		}
	}
	return ErrForbidden
}

func (s *Service) commitDay(day int) {
	for {
		current := s.clock.Load()
		if current != nil && day <= *current {
			return
		}
		value := day
		if current == nil {
			if s.clock.CompareAndSwap(nil, &value) {
				return
			}
			continue
		}
		if s.clock.CompareAndSwap(current, &value) {
			return
		}
	}
}
