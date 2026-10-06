package enrollment

func (e *Engine) SetCapacity(sectionID string, capacity int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if sectionID == "" || capacity < 0 {
		return ruleError(ErrInvalidArgument, "section id and non-negative capacity are required")
	}
	section, ok := e.sections[sectionID]
	if !ok {
		return ruleError(ErrSectionNotFound, "section does not exist")
	}
	section.Capacity = capacity
	e.sections[sectionID] = section
	if sections := e.courseSection[section.CourseID]; sections != nil {
		sections[sectionID] = section
	}
	return nil
}

func (e *Engine) CapacityAvailable(sectionID string) (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	section, ok := e.sections[sectionID]
	if !ok {
		return false, ruleError(ErrSectionNotFound, "section does not exist")
	}
	return e.counts[sectionID] < section.Capacity, nil
}

func (e *Engine) HasTimeConflict(studentID, sectionID string) (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if studentID == "" || sectionID == "" {
		return false, ruleError(ErrInvalidArgument, "student id and section id are required")
	}
	section, ok := e.sections[sectionID]
	if !ok {
		return false, ruleError(ErrSectionNotFound, "section does not exist")
	}
	if student, ok := e.students[studentID]; ok {
		for _, timeSlot := range section.Times {
			if student.busyTimes[timeSlot] {
				return true, nil
			}
		}
	}
	return false, nil
}
