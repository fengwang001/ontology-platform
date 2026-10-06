package loto

import "fmt"

// BeginTrial 开工后持票人申请试运行：
// 须全员离场；本票所有作业人员的锁暂时解除（保留记录，物理隔离中移除）。
// 共享隔离点上若还有其他票的锁，该点仍保持隔离——这一点由全局 lockOwners 索引自然保证。
func (s *System) BeginTrial(permitID, actor string, at int64) error {
	if permitID == "" || actor == "" {
		return fail(InvalidParam, "permit and actor are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	p, ok := s.st.permits[permitID]
	if !ok {
		return fail(NotFound, "permit %q not found", permitID)
	}
	if _, ok := s.st.persons[actor]; !ok {
		return fail(NotFound, "actor %q not registered", actor)
	}
	if actor != p.Applicant {
		return fail(PermissionDenied, "only permit holder %q may request a trial run", p.Applicant)
	}
	if p.Phase != PhaseWorking {
		return fail(StateNotAllowed, "permit %q phase=%s; trial run only while working", permitID, p.Phase)
	}
	if p.Overdue {
		return fail(StateNotAllowed, "permit %q is overdue; trial run closed", permitID)
	}
	if len(p.Inside) != 0 {
		return fail(ConditionNotMet, "cannot begin trial: %d worker(s) still inside", len(p.Inside))
	}

	s.advanceClock(at)
	released := 0
	for key, lk := range p.Locks {
		if lk.Removed || lk.TrialRemoved {
			continue
		}
		_ = key
		lk.TrialRemoved = true
		delete(s.st.lockOwners[lk.Point], lockKey(permitID, lk.Worker))
		released++
	}
	p.Phase = PhaseTrial

	// 记录每个共享点上还压着哪些他票锁（解释为何该点仍隔离）。
	var stillIsolated []string
	for pt := range p.Points {
		if n := len(s.st.lockOwners[pt]); n > 0 {
			stillIsolated = append(stillIsolated, fmt.Sprintf("%s:%d", pt, n))
		}
	}
	s.log(at, "begin_trial", actor, permitID,
		fmt.Sprintf("releasedLocks=%d (records kept); shared points still isolated by others=%v",
			released, stillIsolated))
	return nil
}

// EndTrial 试运行结束：原持锁人逐个重新上锁的语义是"逐把恢复"；
// 本调用要求所有暂解的锁均已通过 PlaceLock 恢复（逾期票同理）。
// 全部恢复后票回到已验证状态（这里是 effective 态的"已上锁"子状态），
// 必须再次执行 Verify 零能量验证才能重新开工。
func (s *System) EndTrial(permitID, actor string, at int64) error {
	if permitID == "" || actor == "" {
		return fail(InvalidParam, "permit and actor are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	p, ok := s.st.permits[permitID]
	if !ok {
		return fail(NotFound, "permit %q not found", permitID)
	}
	if _, ok := s.st.persons[actor]; !ok {
		return fail(NotFound, "actor %q not registered", actor)
	}
	if actor != p.Applicant {
		return fail(PermissionDenied, "only permit holder %q may end a trial run", p.Applicant)
	}
	if p.Phase != PhaseTrial {
		return fail(StateNotAllowed, "permit %q phase=%s; end-trial only from trial", permitID, p.Phase)
	}
	var missing []string
	for _, lk := range p.Locks {
		if lk.TrialRemoved && !lk.Removed {
			missing = append(missing, lk.Worker+"@"+lk.Point)
		}
	}
	if len(missing) != 0 {
		return fail(ConditionNotMet, "trial locks not restored by original owners; missing=%d", len(missing))
	}
	s.advanceClock(at)
	p.Phase = PhaseEffective
	s.log(at, "end_trial", actor, permitID,
		"all locks restored by original owners; back to locked state, re-verification required")
	return nil
}

// ForceRemoveLock 逾期票的主管强制摘除：
// 仅适用于逾期且未完工的票；须附非空理由，并由另一名不同主管确认。
// 每次强制摘除记入审计；被摘锁人重新进入现场前须重新上锁（由 Enter 检查）。
func (s *System) ForceRemoveLock(permitID, point, worker, supervisor, confirmer, reason string, at int64) error {
	if permitID == "" || point == "" || worker == "" || supervisor == "" || confirmer == "" {
		return fail(InvalidParam, "permit, point, worker, supervisor and confirmer are required")
	}
	if reason == "" {
		return fail(InvalidParam, "force removal requires a non-empty reason")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	p, ok := s.st.permits[permitID]
	if !ok {
		return fail(NotFound, "permit %q not found", permitID)
	}
	sup, ok := s.st.persons[supervisor]
	if !ok {
		return fail(NotFound, "supervisor %q not registered", supervisor)
	}
	con, ok := s.st.persons[confirmer]
	if !ok {
		return fail(NotFound, "confirmer %q not registered", confirmer)
	}
	if _, ok := s.st.persons[worker]; !ok {
		return fail(NotFound, "worker %q not registered", worker)
	}
	lk := p.Locks[worker+"\x00"+point]
	if lk == nil {
		return fail(NotFound, "no lock of %q on point %q for permit %q", worker, point, permitID)
	}
	if !sup.Roles[RoleSupervisor] {
		return fail(PermissionDenied, "%q lacks role supervisor", supervisor)
	}
	if !con.Roles[RoleSupervisor] {
		return fail(PermissionDenied, "confirmer %q lacks role supervisor", confirmer)
	}
	if !p.Workers[worker] {
		return fail(PermissionDenied, "%q is not a worker of permit %q", worker, permitID)
	}
	if !p.Overdue || p.Phase == PhaseCompleted {
		return fail(StateNotAllowed, "force removal only on overdue, non-completed permit (phase=%s overdue=%v)", p.Phase, p.Overdue)
	}
	if lk.Removed {
		return fail(StateNotAllowed, "that lock is already removed")
	}
	if supervisor == confirmer {
		return fail(ConditionNotMet, "confirmer must be a different supervisor from %q", supervisor)
	}

	s.advanceClock(at)
	wasTrial := lk.TrialRemoved
	lk.Removed = true
	lk.TrialRemoved = false
	lk.Forced = true
	delete(s.st.lockOwners[point], lockKey(permitID, worker))
	remaining := len(s.st.lockOwners[point])
	s.log(at, "force_remove_lock", supervisor, permitID,
		fmt.Sprintf("point=%s worker=%s confirmer=%s reason=%q trialRemovedBefore=%v remainingLocks=%d",
			point, worker, confirmer, reason, wasTrial, remaining))
	return nil
}
