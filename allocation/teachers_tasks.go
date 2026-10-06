package allocation

func (s *Service) AddTeacher(now int64, spec TeacherSpec) *Error {
	if spec.ID == "" || spec.Rank == "" {
		return newErr(ErrInvalidArgument, "empty teacher id/rank")
	}
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if err := svc.checkClock(now); err != nil {
		return err
	}
	if _, ok := svc.rankLimit(spec.Rank); !ok {
		return newErr(ErrInvalidArgument, "unknown rank %q", spec.Rank)
	}
	if _, exists := svc.teachers[spec.ID]; exists {
		return newErr(ErrInvalidArgument, "duplicate teacher %q", spec.ID)
	}
	svc.teachers[spec.ID] = &teacher{
		id: spec.ID, rank: spec.Rank, sched: newSchedule(),
		holding: make(map[int64]bool), used: make(map[string]int),
	}
	svc.clock = now
	svc.trace("AddTeacher now=%d spec=%+v -> ok", now, spec)
	return nil
}

func (s *Service) AddTask(now int64, spec TaskSpec) *Error {
	if spec.ID == "" || spec.Semester == "" || spec.Hours <= 0 || spec.ClassSize < 0 {
		return newErr(ErrInvalidArgument, "bad task fields")
	}
	if spec.StartWeek <= 0 || spec.EndWeek < spec.StartWeek || spec.EndWeek > maxWeek {
		return newErr(ErrInvalidArgument, "bad week range [%d,%d]", spec.StartWeek, spec.EndWeek)
	}
	weeks := spec.EndWeek - spec.StartWeek + 1
	if spec.Hours < weeks {
		return newErr(ErrInvalidArgument,
			"hours %d < weeks %d: uniform distribution would produce a zero-hour week", spec.Hours, weeks)
	}
	periods, err := uniqueSortedPeriods(spec.Periods)
	if err != nil {
		return err
	}
	spec.Periods = periods

	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if err := svc.checkClock(now); err != nil {
		return err
	}
	if _, exists := svc.tasks[spec.ID]; exists {
		return newErr(ErrInvalidArgument, "duplicate task %q", spec.ID)
	}
	scalePct, err := scalePercent(svc.cfg.Tiers, spec.ClassSize)
	if err != nil {
		return err
	}
	full, err := taskWorkload(svc.cfg, spec.Hours, scalePct, spec.IsNew, spec.IsLab)
	if err != nil {
		return err
	}
	svc.tasks[spec.ID] = &task{spec: spec, scalePct: scalePct, workload: full}
	svc.clock = now
	svc.trace("AddTask now=%d id=%s hours=%d size=%d new=%v lab=%v weeks=[%d,%d] periods=%v scale=%d%% fullWL=%d -> ok",
		now, spec.ID, spec.Hours, spec.ClassSize, spec.IsNew, spec.IsLab,
		spec.StartWeek, spec.EndWeek, spec.Periods, scalePct, full)
	return nil
}
