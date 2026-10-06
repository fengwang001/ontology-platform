package specimen

func nonEmpty(value string) bool {
	return value != ""
}

func validRequirement(requirement CatalogRequirement) bool {
	return nonEmpty(requirement.TubeType) &&
		requirement.MaxDeliverySeconds > 0 &&
		requirement.HemolysisTolerance >= 0 &&
		requirement.HemolysisTolerance <= 4
}

func validateProjectIDs(projectIDs []string) bool {
	if len(projectIDs) < 1 || len(projectIDs) > 10 {
		return false
	}
	seen := make(map[string]struct{}, len(projectIDs))
	for _, projectID := range projectIDs {
		if !nonEmpty(projectID) {
			return false
		}
		if _, duplicated := seen[projectID]; duplicated {
			return false
		}
		seen[projectID] = struct{}{}
	}
	return true
}

func (s *System) checkClock(now int64) error {
	if now < s.now {
		return errorf(CodeClockRollback, "now=%d 小于已接受时刻 %d", now, s.now)
	}
	return nil
}

func (s *System) setClock(now int64) {
	s.now = now
}
