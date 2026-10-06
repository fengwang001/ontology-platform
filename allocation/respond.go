package allocation

func (s *Service) Confirm(now int64, shareID int64) *Error {
	return s.respond(now, shareID, true)
}

func (s *Service) Reject(now int64, shareID int64) *Error {
	return s.respond(now, shareID, false)
}

func (s *Service) respond(now int64, shareID int64, accept bool) *Error {
	if shareID <= 0 {
		return newErr(ErrInvalidArgument, "bad share id")
	}
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if err := svc.checkClock(now); err != nil {
		return err
	}
	sh, ok := svc.shares[shareID]
	if !ok {
		return newErr(ErrNotFound, "share %d not found", shareID)
	}
	svc.expire(svc.teachers[sh.teacherID], now)
	switch sh.state {
	case statePending:
		if accept {
			sh.state = stateActive
			svc.trace("Confirm now=%d share=%d -> active (deadline=%d, now<=deadline: %v)",
				now, shareID, sh.deadline, now <= sh.deadline)
		} else {
			sh.state = stateRejected
			t := svc.teachers[sh.teacherID]
			tk := svc.tasks[sh.taskID]
			t.sched.remove(sh.startWeek, sh.endWeek, tk.spec.Periods)
			delete(t.holding, sh.id)
			t.used[sh.semester] -= sh.workload
			svc.trace("Reject now=%d share=%d -> rejected", now, shareID)
		}
		svc.clock = now
		return nil
	case stateActive:
		return newErr(ErrState, "share %d already active", shareID)
	case stateRejected:
		return newErr(ErrState, "share %d already rejected", shareID)
	default: // released
		return newErr(ErrState, "share %d already released and cannot be confirmed", shareID)
	}
}
