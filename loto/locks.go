package loto

import "fmt"

// allLocksPresent：票内每位作业人员是否已对全部隔离点挂出物理在位的锁。
func (p *Permit) allLocksPresent() bool {
	for w := range p.Workers {
		for pt := range p.Points {
			lk := p.Locks[w+"\x00"+pt]
			if lk == nil || lk.Removed || lk.TrialRemoved {
				return false
			}
		}
	}
	return true
}

// workerLockComplete：某作业人员是否已完成自己对全部隔离点的上锁。
func (p *Permit) workerLockComplete(w string) bool {
	for pt := range p.Points {
		lk := p.Locks[w+"\x00"+pt]
		if lk == nil || lk.Removed || lk.TrialRemoved {
			return false
		}
	}
	return true
}

// PlaceLock 作业人员本人在某隔离点上为指定票挂一把自己的锁。
// 同一人在同一隔离点上对同一张票只能有一把锁。
func (s *System) PlaceLock(permitID, worker, point string, at int64) error {
	if permitID == "" || worker == "" || point == "" {
		return fail(InvalidParam, "permit, worker and point are required")
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
	if !p.Points[point] {
		return fail(InvalidParam, "point %q is not required by permit %q", point, permitID)
	}
	if _, ok := s.st.persons[worker]; !ok {
		return fail(NotFound, "worker %q not registered", worker)
	}
	if !p.Workers[worker] {
		return fail(PermissionDenied, "%q is not a registered worker of permit %q", worker, permitID)
	}
	switch p.Phase {
	case PhaseEffective, PhaseVerified:
	case PhaseWorking:
		// 已开工后只允许"重新上锁"：锁此前被主管强制摘除（Forced）。
		lk := p.Locks[worker+"\x00"+point]
		if lk == nil || !lk.Removed || !lk.Forced {
			return fail(StateNotAllowed, "permit %q is working; lock placement only allowed to restore a force-removed lock", permitID)
		}
	case PhaseTrial:
		// 试运行结束阶段：由原持锁人逐个恢复自己暂解的锁。
		lk := p.Locks[worker+"\x00"+point]
		if lk == nil || !lk.TrialRemoved || lk.Worker != worker {
			return fail(StateNotAllowed, "during trial only the original owner may restore that owner's temporarily removed lock")
		}
	default:
		return fail(StateNotAllowed, "permit %q phase=%s does not allow locking", permitID, p.Phase)
	}
	key := worker + "\x00" + point
	if lk := p.Locks[key]; lk != nil && !lk.Removed && !lk.TrialRemoved {
		return fail(StateNotAllowed, "worker %q already holds a lock on point %q for permit %q", worker, point, permitID)
	}

	s.advanceClock(at)
	var lk *Lock
	if lk = p.Locks[key]; lk == nil {
		lk = &Lock{Point: point, Worker: worker, Permit: permitID}
		p.Locks[key] = lk
	}
	lk.Removed = false
	lk.TrialRemoved = false
	lk.Forced = false

	owners := s.st.lockOwners[point]
	if owners == nil {
		owners = map[string]bool{}
		s.st.lockOwners[point] = owners
	}
	owners[lockKey(permitID, worker)] = true

	s.log(at, "place_lock", worker, permitID, fmt.Sprintf("point=%s physicalLocks=%d", point, len(owners)))
	return nil
}

// Verify 由验证人执行零能量验证。验证人不得是该票任何作业人员。
// 前提：全员完成全部上锁。试运行恢复后须再次验证。
func (s *System) Verify(permitID, verifier string, at int64) error {
	if permitID == "" || verifier == "" {
		return fail(InvalidParam, "permit and verifier are required")
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
	if _, ok := s.st.persons[verifier]; !ok {
		return fail(NotFound, "verifier %q not registered", verifier)
	}
	if p.Phase != PhaseEffective {
		return fail(StateNotAllowed, "permit %q phase=%s; verify only after full locking (or trial restore)", permitID, p.Phase)
	}
	if p.Workers[verifier] {
		return fail(ConditionNotMet, "verifier %q must not be a worker of permit %q", verifier, permitID)
	}
	if !p.allLocksPresent() {
		var missing []string
		for w := range p.Workers {
			for pt := range p.Points {
				lk := p.Locks[w+"\x00"+pt]
				if lk == nil || lk.Removed || lk.TrialRemoved {
					missing = append(missing, w+"@"+pt)
				}
			}
		}
		return fail(ConditionNotMet, "not all workers locked every point; missing=%d e.g.=%v", len(missing), missing)
	}
	s.advanceClock(at)
	p.Phase = PhaseVerified
	s.log(at, "verify_zero_energy", verifier, permitID,
		fmt.Sprintf("all %d workers locked %d points; isolation confirmed", len(p.Workers), len(p.Points)))
	return nil
}

// StartWork 开工。开工时刻须落在计划时段 [start,end) 内。
func (s *System) StartWork(permitID, actor string, at int64) error {
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
		return fail(PermissionDenied, "only applicant %q may start work", p.Applicant)
	}
	if p.Phase != PhaseVerified {
		return fail(StateNotAllowed, "permit %q phase=%s; start only from verified", permitID, p.Phase)
	}
	if p.Overdue || at >= p.End {
		return fail(StateNotAllowed, "permit %q is overdue / past planned end", permitID)
	}
	if at < p.Start || at >= p.End {
		return fail(ConditionNotMet, "start time %d outside planned window [%d,%d)", at, p.Start, p.End)
	}
	s.advanceClock(at)
	p.Phase = PhaseWorking
	s.log(at, "start_work", actor, permitID, "verified state -> working")
	return nil
}

// Enter 作业人员进入现场。须已完成自己的全部上锁且票已开工；
// 被强制摘除锁的人再次进入前必须重新上锁。离场后可再次进入。
func (s *System) Enter(permitID, worker string, at int64) error {
	if permitID == "" || worker == "" {
		return fail(InvalidParam, "permit and worker are required")
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
	if _, ok := s.st.persons[worker]; !ok {
		return fail(NotFound, "worker %q not registered", worker)
	}
	if !p.Workers[worker] {
		return fail(PermissionDenied, "%q is not a worker of permit %q", worker, permitID)
	}
	if p.Phase != PhaseWorking {
		return fail(StateNotAllowed, "permit %q phase=%s; enter only while working", permitID, p.Phase)
	}
	if p.Inside[worker] {
		return fail(ConditionNotMet, "worker %q is already inside", worker)
	}
	if !p.workerLockComplete(worker) {
		return fail(ConditionNotMet, "worker %q must re-lock all points (force-removed lock) before re-entering", worker)
	}
	s.advanceClock(at)
	p.Inside[worker] = true
	p.EverInside = true
	s.log(at, "enter", worker, permitID, fmt.Sprintf("inside=%d/%d", len(p.Inside), len(p.Workers)))
	return nil
}

// Leave 作业人员离场（之后可再次进入）。
func (s *System) Leave(permitID, worker string, at int64) error {
	if permitID == "" || worker == "" {
		return fail(InvalidParam, "permit and worker are required")
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
	if _, ok := s.st.persons[worker]; !ok {
		return fail(NotFound, "worker %q not registered", worker)
	}
	if !p.Workers[worker] {
		return fail(PermissionDenied, "%q is not a worker of permit %q", worker, permitID)
	}
	if p.Phase != PhaseWorking {
		return fail(StateNotAllowed, "permit %q phase=%s; leave only while working", permitID, p.Phase)
	}
	if !p.Inside[worker] {
		return fail(ConditionNotMet, "worker %q is not inside", worker)
	}
	s.advanceClock(at)
	delete(p.Inside, worker)
	s.log(at, "leave", worker, permitID, fmt.Sprintf("inside=%d/%d", len(p.Inside), len(p.Workers)))
	return nil
}

// Complete 完工：须所有人均已离场。由申请人（持票人）执行。
func (s *System) Complete(permitID, actor string, at int64) error {
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
		return fail(PermissionDenied, "only applicant %q may complete the permit", p.Applicant)
	}
	if p.Phase != PhaseWorking {
		return fail(StateNotAllowed, "permit %q phase=%s; complete only from working", permitID, p.Phase)
	}
	if at >= p.End {
		return fail(StateNotAllowed, "permit %q reached planned end before completion (overdue); normal completion closed", permitID)
	}
	if len(p.Inside) != 0 {
		return fail(ConditionNotMet, "cannot complete: %d worker(s) still inside", len(p.Inside))
	}
	s.advanceClock(at)
	p.Phase = PhaseCompleted
	p.CompletedAt = at
	for d := range p.Devices {
		delete(s.st.activeByDevice[d], permitID)
	}
	s.log(at, "complete", actor, permitID, "all workers left; permit completed (locks remain until removed)")
	return nil
}

// RemoveLock 完工后持锁人本人摘除自己的锁；只有摘除隔离点上最后一把锁，该点才解除隔离。
// 逾期票的锁不能由本人摘除（须走主管强制摘除流程）。
func (s *System) RemoveLock(permitID, worker, point string, at int64) error {
	if permitID == "" || worker == "" || point == "" {
		return fail(InvalidParam, "permit, worker and point are required")
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
	if _, ok := s.st.persons[worker]; !ok {
		return fail(NotFound, "worker %q not registered", worker)
	}
	lk := p.Locks[worker+"\x00"+point]
	if lk == nil {
		return fail(NotFound, "no lock of worker %q on point %q for permit %q", worker, point, permitID)
	}
	if !p.Workers[worker] {
		return fail(PermissionDenied, "%q is not a worker of permit %q", worker, permitID)
	}
	if p.Overdue && p.Phase != PhaseCompleted {
		return fail(PermissionDenied, "permit %q is overdue: personal removal forbidden; supervisor force-removal required", permitID)
	}
	if p.Phase != PhaseCompleted {
		return fail(StateNotAllowed, "permit %q phase=%s; locks may be removed only after completion", permitID, p.Phase)
	}
	if lk.Removed {
		return fail(StateNotAllowed, "lock already removed")
	}

	s.advanceClock(at)
	lk.Removed = true
	delete(s.st.lockOwners[point], lockKey(permitID, worker))
	remaining := len(s.st.lockOwners[point])
	isolation := "point stays isolated"
	if remaining == 0 {
		isolation = "LAST lock removed: isolation released"
	}
	s.log(at, "remove_lock", worker, permitID,
		fmt.Sprintf("point=%s remainingLocks=%d (%s)", point, remaining, isolation))
	return nil
}
