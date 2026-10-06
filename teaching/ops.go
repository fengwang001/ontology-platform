package teaching

import "sort"

// AddTeacher 注册教师。时钟不得回退（相等允许）。
func (s *Service) AddTeacher(id, rankName string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || rankName == "" {
		return errf(ErrInvalidParameter, "empty teacher id or rank")
	}
	if now < s.now {
		return errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	oldNow := s.now
	s.now = now
	swept := s.sweepExpiredLocked()
	rank, ok := s.cfg.Ranks[rankName]
	if !ok {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrInvalidParameter, "unknown rank %q", rankName)
	}
	if _, exists := s.teachers[id]; exists {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrIllegalState, "teacher %q already exists", id)
	}
	s.teachers[id] = struct{}{}
	r := rank
	s.ranks[id] = &r
	return nil
}

// AddTask 注册课程任务。
func (s *Service) AddTask(spec TaskSpec, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateTaskSpec(spec); err != nil {
		return err
	}
	if now < s.now {
		return errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	oldNow := s.now
	s.now = now
	swept := s.sweepExpiredLocked()
	if _, ok := scaleCoeff(s.cfg.Tiers, spec.ClassSize); !ok {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrInvalidParameter, "class size %d matches no tier", spec.ClassSize)
	}
	if _, exists := s.tasks[spec.ID]; exists {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrIllegalState, "task %q already exists", spec.ID)
	}
	cp := spec
	cp.Periods = append([]int(nil), spec.Periods...)
	sort.Ints(cp.Periods)
	s.tasks[spec.ID] = &cp
	s.allocated[spec.ID] = 0
	return nil
}

func validateTaskSpec(spec TaskSpec) error {
	if spec.ID == "" || spec.Semester == "" {
		return errf(ErrInvalidParameter, "empty task id or semester")
	}
	if spec.Hours <= 0 || spec.ClassSize <= 0 {
		return errf(ErrInvalidParameter, "hours and class size must be positive")
	}
	if spec.WeekStart <= 0 || spec.WeekEnd < spec.WeekStart {
		return errf(ErrInvalidParameter, "invalid week interval")
	}
	if len(spec.Periods) == 0 {
		return errf(ErrInvalidParameter, "task needs at least one weekly period")
	}
	seen := map[int]struct{}{}
	for _, p := range spec.Periods {
		if p <= 0 {
			return errf(ErrInvalidParameter, "period must be positive")
		}
		if _, dup := seen[p]; dup {
			return errf(ErrInvalidParameter, "duplicate period %d", p)
		}
		seen[p] = struct{}{}
	}
	weeks := spec.WeekEnd - spec.WeekStart + 1
	if spec.Hours < weeks {
		return errf(ErrInvalidParameter, "hours %d < weeks %d", spec.Hours, weeks)
	}
	return nil
}
