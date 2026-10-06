package enrollment

func (e *Engine) SetCreditLimit(studentID string, limit int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if studentID == "" || limit < 0 {
		return ruleError(ErrInvalidArgument, "student id and non-negative credit limit are required")
	}
	e.student(studentID).creditLimit = limit
	return nil
}

func (e *Engine) SetHistory(studentID, courseID string, score int, passed bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if studentID == "" || courseID == "" {
		return ruleError(ErrInvalidArgument, "student id and course id are required")
	}
	if _, ok := e.courses[courseID]; !ok {
		return ruleError(ErrCourseNotFound, "history course does not exist")
	}
	e.student(studentID).history[courseID] = historyCourse{score: score, passed: passed}
	return nil
}
