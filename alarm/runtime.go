package alarm

import (
	"container/heap"
	"encoding/json"
	"fmt"
)

func (s *Service) validateOperation(name string, op Operation) error {
	if op.At < 0 {
		return newError(InvalidArgument, name, "time is negative")
	}
	if op.PointID == "" {
		return newError(InvalidArgument, name, "point id is empty")
	}
	if op.Role != Operator && op.Role != Engineer {
		return newError(InvalidArgument, name, "invalid role")
	}
	switch name {
	case "manual suppress":
		if op.Duration <= 0 {
			return newError(InvalidArgument, name, "duration must be positive")
		}
		if op.Reason == "" {
			return newError(InvalidArgument, name, "reason is empty")
		}
	case "disable", "enable":
		if op.Ticket == "" {
			return newError(InvalidArgument, name, "change ticket is empty")
		}
	}
	return nil
}

func (s *Service) beginPointOperation(name string, op Operation, apply func(*point) Result) (Result, error) {
	if err := s.validateOperation(name, op); err != nil {
		s.log(name, op, nil, nil, err.Error())
		return Result{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if op.At < s.lastClock {
		err := newError(ClockRewound, name, fmt.Sprintf("time %d before last clock %d", op.At, s.lastClock))
		s.log(name, op, nil, nil, err.Error())
		return Result{}, err
	}
	runtimePoint, exists := s.points[op.PointID]
	if !exists {
		err := newError(PointNotFound, name, fmt.Sprintf("point %q does not exist", op.PointID))
		s.log(name, op, nil, nil, err.Error())
		return Result{}, err
	}
	if (name == "disable" || name == "enable") && op.Role != Engineer {
		err := newError(PermissionDenied, name, "engineer role required")
		s.log(name, op, runtimePoint, nil, err.Error())
		return Result{}, err
	}
	if (name == "trigger" || name == "return") && runtimePoint.disabled {
		s.lastClock = op.At
		result := Result{Ignored: true, Visible: s.activity.contains(op.PointID), State: runtimePoint.state}
		s.log(name, op, runtimePoint, result, "point is disabled; event ignored")
		return result, nil
	}
	if err := s.checkPointState(name, op, runtimePoint); err != nil {
		s.log(name, op, runtimePoint, nil, err.Error())
		return Result{}, err
	}

	s.lastClock = op.At
	s.expire(op.At)
	return apply(runtimePoint), nil
}

func (s *Service) checkPointState(name string, op Operation, runtimePoint *point) error {
	switch name {
	case "manual suppress":
		if runtimePoint.config.Priority == Emergency {
			return newError(StateNotAllowed, name, "emergency alarms cannot be manually suppressed")
		}
		if runtimePoint.disabled {
			return newError(StateNotAllowed, name, "disabled point cannot be suppressed")
		}
		if runtimePoint.suppressed(op.At) {
			return newError(StateNotAllowed, name, "point is already suppressed")
		}
		limit := s.config.HighManualDuration
		if runtimePoint.config.Priority == Low {
			limit = s.config.LowManualDuration
		}
		if op.Duration > limit {
			return newError(DurationLimitExceeded, name, fmt.Sprintf("duration %d exceeds limit %d", op.Duration, limit))
		}
	case "release suppression":
		if !runtimePoint.suppressed(op.At) {
			return newError(StateNotAllowed, name, "point is not suppressed")
		}
	case "disable":
		if runtimePoint.disabled {
			return newError(StateNotAllowed, name, "point is already disabled")
		}
	case "enable":
		if !runtimePoint.disabled {
			return newError(StateNotAllowed, name, "point is not disabled")
		}
	case "return":
		if runtimePoint.state != ActiveUnacknowledged && runtimePoint.state != ActiveAcknowledged {
			return newError(StateNotAllowed, name, "alarm is not active")
		}
	case "acknowledge":
		if runtimePoint.state != ActiveUnacknowledged && runtimePoint.state != ReturnedUnacknowledged {
			return newError(StateNotAllowed, name, "state does not allow acknowledgement")
		}
	}
	return nil
}

func (s *Service) expire(at int64) {
	for s.expiries.Len() > 0 && (*s.expiries)[0].until <= at {
		item := heap.Pop(s.expiries).(expiryItem)
		runtimePoint := s.points[item.pointID]
		if runtimePoint.suppressionVersion != item.version || runtimePoint.suppressedUntil != item.until {
			continue
		}
		runtimePoint.suppressedUntil = 0
		runtimePoint.suppressionVersion++
		runtimePoint.suppressionReason = ""
		s.reconcile(runtimePoint, item.until)
		s.log("suppression expired", item, runtimePoint, Result{Visible: s.activity.contains(item.pointID), State: runtimePoint.state}, "scheduled suppression reached its end time")
	}
}

func (s *Service) discardExpiredForPoint(pointID string, at int64) {
	runtimePoint := s.points[pointID]
	for index := 0; index < s.expiries.Len(); index++ {
		item := (*s.expiries)[index]
		if item.pointID != pointID || item.until > at ||
			item.version != runtimePoint.suppressionVersion || item.until != runtimePoint.suppressedUntil {
			continue
		}
		heap.Remove(s.expiries, index)
		runtimePoint.suppressedUntil = 0
		runtimePoint.suppressionVersion++
		runtimePoint.suppressionReason = ""
		break
	}
}

func (s *Service) reconcile(runtimePoint *point, at int64) {
	if visibleState(runtimePoint.state) && !runtimePoint.hidden(at) {
		entry := runtimePoint.activeEntry()
		if s.activity.contains(runtimePoint.config.ID) {
			s.activity.update(entry)
		} else {
			s.activity.show(entry, at)
		}
		return
	}
	s.activity.hide(runtimePoint.config.ID)
}

func (s *Service) log(action string, input any, runtimePoint *point, output any, reason string) {
	if s.logger == nil {
		return
	}
	record := map[string]any{
		"action": action,
		"input":  input,
		"output": output,
		"reason": reason,
	}
	if runtimePoint != nil {
		record["point"] = map[string]any{
			"id":                     runtimePoint.config.ID,
			"state":                  runtimePoint.state,
			"last_activation":        runtimePoint.lastActivation,
			"suppressed_until":       runtimePoint.suppressedUntil,
			"condition_inhibited":    runtimePoint.conditionInhibited,
			"disabled":               runtimePoint.disabled,
			"unacknowledged_on_show": runtimePoint.unacknowledgedOnShow,
		}
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	_, _ = s.logger.Write(append(encoded, '\n'))
}
