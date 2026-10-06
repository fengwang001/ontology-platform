package alarm

import (
	"container/heap"
)

func (s *Service) Trigger(op Operation) (Result, error) {
	return s.beginPointOperation("trigger", op, func(runtimePoint *point) Result {
		if runtimePoint.disabled {
			result := Result{Ignored: true, Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
			s.log("trigger", op, runtimePoint, result, "point is disabled; trigger ignored")
			return result
		}

		newState, enteredActivation := applyTrigger(runtimePoint.state)
		runtimePoint.state = newState
		autoSuppressed := false
		if enteredActivation {
			runtimePoint.lastActivation = op.At
			if runtimePoint.hidden(op.At) {
				runtimePoint.unacknowledgedOnShow = true
			} else if runtimePoint.config.Priority != Emergency &&
				runtimePoint.chatter.activate(op.At, s.config.ChatterWindow, s.config.ChatterCount) {
				runtimePoint.suppressedUntil = op.At + s.config.ChatterDuration
				runtimePoint.suppressionVersion++
				runtimePoint.suppressionReason = ChatterReason
				heap.Push(s.expiries, expiryItem{
					pointID: op.PointID,
					until:   runtimePoint.suppressedUntil,
					version: runtimePoint.suppressionVersion,
				})
				runtimePoint.unacknowledgedOnShow = true
				autoSuppressed = true
			}
		}

		s.reconcile(runtimePoint, op.At)
		result := Result{Ignored: !enteredActivation, Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
		reason := "trigger entered active unacknowledged"
		if !enteredActivation {
			reason = "alarm already active; duplicate trigger made no change"
		} else if autoSuppressed {
			reason = "chatter threshold reached; automatic suppression applied"
		}
		s.log("trigger", op, runtimePoint, result, reason)
		return result
	})
}

func (s *Service) ReturnToNormal(op Operation) (Result, error) {
	return s.beginPointOperation("return", op, func(runtimePoint *point) Result {
		if runtimePoint.disabled {
			result := Result{Ignored: true, Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
			s.log("return", op, runtimePoint, result, "point is disabled; return ignored")
			return result
		}

		newState, _ := applyReturn(runtimePoint.state)
		if newState == Normal && runtimePoint.unacknowledgedOnShow {
			newState = ReturnedUnacknowledged
		}
		runtimePoint.state = newState
		s.reconcile(runtimePoint, op.At)
		result := Result{Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
		s.log("return", op, runtimePoint, result, "return transition applied")
		return result
	})
}

func (s *Service) Acknowledge(op Operation) (Result, error) {
	return s.beginPointOperation("acknowledge", op, func(runtimePoint *point) Result {
		wasReturnedUnacknowledged := runtimePoint.state == ReturnedUnacknowledged
		runtimePoint.state, _ = applyAcknowledge(runtimePoint.state)
		if wasReturnedUnacknowledged {
			runtimePoint.unacknowledgedOnShow = false
		}
		s.reconcile(runtimePoint, op.At)
		result := Result{Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
		s.log("acknowledge", op, runtimePoint, result, "acknowledgement applied")
		return result
	})
}

func (s *Service) Suppress(op Operation) (Result, error) {
	return s.beginPointOperation("manual suppress", op, func(runtimePoint *point) Result {
		s.discardExpiredForPoint(op.PointID, op.At)
		runtimePoint.suppressedUntil = op.At + op.Duration
		runtimePoint.suppressionVersion++
		runtimePoint.suppressionReason = op.Reason
		heap.Push(s.expiries, expiryItem{
			pointID: op.PointID,
			until:   runtimePoint.suppressedUntil,
			version: runtimePoint.suppressionVersion,
		})
		s.reconcile(runtimePoint, op.At)
		result := Result{Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
		s.log("manual suppress", op, runtimePoint, result, "manual suppression applied")
		return result
	})
}

func (s *Service) ReleaseSuppression(op Operation) (Result, error) {
	return s.beginPointOperation("release suppression", op, func(runtimePoint *point) Result {
		runtimePoint.suppressedUntil = op.At
		runtimePoint.suppressionVersion++
		runtimePoint.suppressionReason = ""
		s.reconcile(runtimePoint, op.At)
		result := Result{Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
		s.log("release suppression", op, runtimePoint, result, "suppression released before expiry")
		return result
	})
}

func (s *Service) Disable(op Operation) (Result, error) {
	return s.beginPointOperation("disable", op, func(runtimePoint *point) Result {
		runtimePoint.disabled = true
		s.reconcile(runtimePoint, op.At)
		result := Result{Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
		s.log("disable", op, runtimePoint, result, "point disabled")
		return result
	})
}

func (s *Service) Enable(op Operation) (Result, error) {
	return s.beginPointOperation("enable", op, func(runtimePoint *point) Result {
		runtimePoint.disabled = false
		runtimePoint.state = Normal
		runtimePoint.lastActivation = 0
		runtimePoint.unacknowledgedOnShow = false
		s.reconcile(runtimePoint, op.At)
		result := Result{Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
		s.log("enable", op, runtimePoint, result, "point enabled and initialized to normal")
		return result
	})
}

func (s *Service) ActiveAlarms(at int64) ([]ActiveAlarm, error) {
	if at < 0 {
		return nil, newError(InvalidArgument, "active alarms", "time is negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastClock = at
	s.expire(at)
	entries := s.activity.snapshot()
	s.log("active alarms", map[string]int64{"at": at}, nil, entries, "snapshot contains only currently visible non-normal alarms")
	return entries, nil
}

func (s *Service) AlarmRate(at int64, duration int64) (int, error) {
	if at < 0 {
		return 0, newError(InvalidArgument, "alarm rate", "time is negative")
	}
	if duration <= 0 {
		return 0, newError(InvalidArgument, "alarm rate", "duration must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastClock = at
	s.expire(at)
	rate := s.activity.rate(at, duration)
	s.log("alarm rate", map[string]int64{"at": at, "duration": duration}, nil, rate, "counted appearances in interval (at-duration, at]")
	return rate, nil
}
