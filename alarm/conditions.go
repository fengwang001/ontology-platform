package alarm

import (
	"fmt"
	"sort"
)

func (s *Service) SetCondition(update ConditionUpdate) error {
	if update.At < 0 {
		return newError(InvalidArgument, "set condition", "time is negative")
	}
	if update.Condition == "" {
		return newError(InvalidArgument, "set condition", "condition name is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if update.At < s.lastClock {
		err := newError(ClockRewound, "set condition", fmt.Sprintf("time %d before last clock %d", update.At, s.lastClock))
		s.log("set condition", update, nil, nil, err.Error())
		return err
	}
	pointIDs := s.conditions[update.Condition]
	if len(pointIDs) == 0 {
		err := newError(PointNotFound, "set condition", fmt.Sprintf("no point uses condition %q", update.Condition))
		s.log("set condition", update, nil, nil, err.Error())
		return err
	}

	s.lastClock = update.At
	s.expire(update.At)
	s.conditionSignals[update.Condition] = update.Active
	affected := make([]*point, 0, len(pointIDs))
	for pointID := range pointIDs {
		affected = append(affected, s.points[pointID])
	}
	sort.Slice(affected, func(left, right int) bool {
		return affected[left].config.ID < affected[right].config.ID
	})

	for _, runtimePoint := range affected {
		if update.Active {
			runtimePoint.conditionInhibited = true
		} else {
			runtimePoint.conditionInhibited = s.anyOtherConditionActive(runtimePoint, update.Condition)
		}
		s.reconcile(runtimePoint, update.At)
	}
	s.log("set condition", update, nil, nil, fmt.Sprintf("condition signal applied to %d configured point(s)", len(affected)))
	return nil
}

func (s *Service) anyOtherConditionActive(runtimePoint *point, changed string) bool {
	for _, name := range runtimePoint.config.Conditions {
		if name != changed && s.conditionSignals[name] {
			return true
		}
	}
	return false
}
