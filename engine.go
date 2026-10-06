package enrollment

import "sync"

type prerequisite struct {
	courseID     string
	minimumScore int
}

type historyCourse struct {
	score  int
	passed bool
}

type selection struct {
	courseID  string
	sectionID string
}

type studentState struct {
	creditLimit int
	selected    map[string]selection
	history     map[string]historyCourse
	credits     int
	busyTimes   map[int]bool
}

type Engine struct {
	mu            sync.RWMutex
	courses       map[string]Course
	sections      map[string]Section
	courseSection map[string]map[string]Section
	prerequisites map[string][]prerequisite
	corequisites  map[string]map[string]bool
	exclusions    map[string]map[string]bool
	students      map[string]*studentState
	counts        map[string]int
}

func NewEngine() *Engine {
	return &Engine{
		courses:       make(map[string]Course),
		sections:      make(map[string]Section),
		courseSection: make(map[string]map[string]Section),
		prerequisites: make(map[string][]prerequisite),
		corequisites:  make(map[string]map[string]bool),
		exclusions:    make(map[string]map[string]bool),
		students:      make(map[string]*studentState),
		counts:        make(map[string]int),
	}
}

func ruleError(code ErrorCode, message string) *RuleError {
	return &RuleError{Code: code, Message: message}
}

func (e *Engine) student(studentID string) *studentState {
	student := e.students[studentID]
	if student == nil {
		student = &studentState{
			selected:  make(map[string]selection),
			history:   make(map[string]historyCourse),
			busyTimes: make(map[int]bool),
		}
		e.students[studentID] = student
	}
	return student
}

func (e *Engine) EnrollBatch(studentID string, requests []Request) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if studentID == "" {
		return ruleError(ErrInvalidArgument, "student id is required")
	}
	student := e.student(studentID)
	pendingCourses := make(map[string]bool, len(requests))
	for index, request := range requests {
		if request.CourseID == "" || request.SectionID == "" {
			return batchError(index, ErrInvalidArgument, "course id and section id are required")
		}
		if pendingCourses[request.CourseID] {
			return batchError(index, ErrInvalidArgument, "course appears twice in one batch")
		}
		pendingCourses[request.CourseID] = true
	}

	pendingSections := make(map[string]bool, len(requests))
	acceptedCourses := make(map[string]bool, len(requests))
	tentativeCredits := student.credits
	for index, request := range requests {
		course, section, err := e.lookupRequest(request)
		if err != nil {
			return withBatchIndex(index, err)
		}
		if _, selected := student.selected[request.CourseID]; selected {
			return batchError(index, ErrAlreadyEnrolled, "course is already selected")
		}
		if err := e.checkExclusions(student, request.CourseID, acceptedCourses); err != nil {
			return withBatchIndex(index, err)
		}
		if err := e.checkPrerequisites(student, request.CourseID); err != nil {
			return withBatchIndex(index, err)
		}
		if err := e.checkCorequisites(student, request.CourseID, pendingCourses); err != nil {
			return withBatchIndex(index, err)
		}
		tentativeCredits += course.Credits
		if tentativeCredits > student.creditLimit {
			return batchError(index, ErrCreditLimit, "student credit limit exceeded")
		}
		if err := e.checkTimes(student, section, pendingSections); err != nil {
			return withBatchIndex(index, err)
		}
		if pendingSections[request.SectionID] || e.counts[request.SectionID] >= section.Capacity {
			return batchError(index, ErrCapacityFull, "section has no available capacity")
		}
		pendingSections[request.SectionID] = true
		acceptedCourses[request.CourseID] = true
	}

	for _, request := range requests {
		course := e.courses[request.CourseID]
		section := e.sections[request.SectionID]
		student.selected[request.CourseID] = selection{courseID: request.CourseID, sectionID: request.SectionID}
		student.credits += course.Credits
		e.counts[request.SectionID]++
		for _, timeSlot := range section.Times {
			student.busyTimes[timeSlot] = true
		}
	}
	return nil
}

func (e *Engine) DropCourse(studentID, courseID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if studentID == "" || courseID == "" {
		return ruleError(ErrInvalidArgument, "student id and course id are required")
	}
	if _, ok := e.courses[courseID]; !ok {
		return ruleError(ErrCourseNotFound, "course does not exist")
	}
	student := e.student(studentID)
	if _, ok := student.selected[courseID]; !ok {
		return ruleError(ErrNotEnrolled, "course is not selected")
	}

	dropped := e.corequisiteComponent(student, courseID)
	for droppedCourseID := range dropped {
		current := student.selected[droppedCourseID]
		section := e.sections[current.sectionID]
		course := e.courses[droppedCourseID]
		e.counts[current.sectionID]--
		student.credits -= course.Credits
		delete(student.selected, droppedCourseID)
		for _, timeSlot := range section.Times {
			delete(student.busyTimes, timeSlot)
		}
	}
	e.rebuildBusyTimes(student)
	return nil
}

func (e *Engine) SwitchSection(studentID string, fromSectionID string, to Request) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if studentID == "" || fromSectionID == "" || to.CourseID == "" || to.SectionID == "" {
		return ruleError(ErrInvalidArgument, "student id, old section and target request are required")
	}
	fromSection, ok := e.sections[fromSectionID]
	if !ok {
		return ruleError(ErrSectionNotFound, "old section does not exist")
	}
	targetCourse, targetSection, err := e.lookupRequest(to)
	if err != nil {
		return err
	}
	if fromSectionID == to.SectionID {
		return ruleError(ErrInvalidArgument, "target section must differ from old section")
	}
	student := e.student(studentID)
	current, selected := student.selected[fromSection.CourseID]
	if !selected || current.sectionID != fromSectionID {
		return ruleError(ErrNotEnrolled, "old section is not selected")
	}
	if _, exists := student.selected[to.CourseID]; exists && to.CourseID != fromSection.CourseID {
		return ruleError(ErrAlreadyEnrolled, "target course is already selected")
	}

	tentativeSelected := make(map[string]selection, len(student.selected))
	for courseID, chosen := range student.selected {
		tentativeSelected[courseID] = chosen
	}
	tentativeCredits := student.credits - e.courses[fromSection.CourseID].Credits
	delete(tentativeSelected, fromSection.CourseID)
	tentativeCredits += targetCourse.Credits
	tentativeSelected[to.CourseID] = selection{courseID: to.CourseID, sectionID: to.SectionID}

	pendingCourses := map[string]bool{to.CourseID: true}
	if err := e.checkExclusionsWithState(student, to.CourseID, pendingCourses, tentativeSelected); err != nil {
		return err
	}
	if err := e.checkPrerequisites(student, to.CourseID); err != nil {
		return err
	}
	for courseID := range tentativeSelected {
		if err := e.checkCorequisitesWithState(courseID, pendingCourses, tentativeSelected); err != nil {
			return err
		}
	}
	if tentativeCredits > student.creditLimit {
		return ruleError(ErrCreditLimit, "student credit limit exceeded")
	}

	tentativeBusy := make(map[int]bool, len(student.busyTimes))
	for timeSlot, busy := range student.busyTimes {
		tentativeBusy[timeSlot] = busy
	}
	for _, timeSlot := range fromSection.Times {
		delete(tentativeBusy, timeSlot)
	}
	for _, timeSlot := range targetSection.Times {
		if tentativeBusy[timeSlot] {
			return ruleError(ErrTimeConflict, "target section time conflicts with selection")
		}
		tentativeBusy[timeSlot] = true
	}
	targetCount := e.counts[to.SectionID] + 1
	if targetCount > targetSection.Capacity {
		return ruleError(ErrCapacityFull, "target section has no available capacity")
	}

	student.credits = tentativeCredits
	delete(student.selected, fromSection.CourseID)
	e.counts[fromSectionID]--
	student.selected[to.CourseID] = selection{courseID: to.CourseID, sectionID: to.SectionID}
	e.counts[to.SectionID]++
	student.busyTimes = tentativeBusy
	return nil
}

func batchError(index int, code ErrorCode, message string) error {
	err := ruleError(code, message)
	err.Index = index
	return err
}

func withBatchIndex(index int, err error) error {
	rule, ok := err.(*RuleError)
	if !ok {
		return err
	}
	rule.Index = index
	return rule
}

func (e *Engine) lookupRequest(request Request) (Course, Section, error) {
	course, ok := e.courses[request.CourseID]
	if !ok {
		return Course{}, Section{}, ruleError(ErrCourseNotFound, "course does not exist")
	}
	section, ok := e.sections[request.SectionID]
	if !ok || section.CourseID != request.CourseID {
		return Course{}, Section{}, ruleError(ErrSectionNotFound, "teaching section does not exist for course")
	}
	return course, section, nil
}

func (e *Engine) checkExclusions(student *studentState, courseID string, pending map[string]bool) error {
	return e.checkExclusionsWithState(student, courseID, pending, student.selected)
}

func (e *Engine) checkExclusionsWithState(student *studentState, courseID string, pending map[string]bool, selected map[string]selection) error {
	for excludedCourseID := range e.exclusions[courseID] {
		if _, isSelected := selected[excludedCourseID]; isSelected {
			return ruleError(ErrMutualExclusion, "course conflicts with a selected mutually exclusive course")
		}
		if pending[excludedCourseID] {
			return ruleError(ErrMutualExclusion, "course conflicts with another course in the batch")
		}
		if history, exists := student.history[excludedCourseID]; exists && history.passed {
			return ruleError(ErrMutualExclusion, "course conflicts with a passed historical course")
		}
	}
	return nil
}

func (e *Engine) checkPrerequisites(student *studentState, courseID string) error {
	for _, required := range e.prerequisites[courseID] {
		history, ok := student.history[required.courseID]
		if !ok || !history.passed || history.score < required.minimumScore {
			return ruleError(ErrPrerequisite, "prerequisite is not satisfied")
		}
	}
	return nil
}

func (e *Engine) checkCorequisites(student *studentState, courseID string, pending map[string]bool) error {
	return e.checkCorequisitesWithState(courseID, pending, student.selected)
}

func (e *Engine) checkCorequisitesWithState(courseID string, pending map[string]bool, selected map[string]selection) error {
	for requiredCourseID := range e.corequisites[courseID] {
		if _, isSelected := selected[requiredCourseID]; isSelected {
			continue
		}
		if pending[requiredCourseID] {
			continue
		}
		return ruleError(ErrCorequisite, "corequisite is not selected in the same batch")
	}
	return nil
}

func (e *Engine) checkTimes(student *studentState, section Section, pendingSections map[string]bool) error {
	pendingTimes := make(map[int]bool)
	for sectionID := range pendingSections {
		for _, timeSlot := range e.sections[sectionID].Times {
			pendingTimes[timeSlot] = true
		}
	}
	for _, timeSlot := range section.Times {
		if student.busyTimes[timeSlot] || pendingTimes[timeSlot] {
			return ruleError(ErrTimeConflict, "section time conflicts with selection")
		}
	}
	return nil
}

func (e *Engine) corequisiteComponent(student *studentState, root string) map[string]bool {
	component := map[string]bool{root: true}
	queue := []string{root}
	for len(queue) > 0 {
		courseID := queue[0]
		queue = queue[1:]
		for relatedCourseID := range e.corequisites[courseID] {
			if _, selected := student.selected[relatedCourseID]; !selected || component[relatedCourseID] {
				continue
			}
			component[relatedCourseID] = true
			queue = append(queue, relatedCourseID)
		}
	}
	return component
}

func (e *Engine) rebuildBusyTimes(student *studentState) {
	busy := make(map[int]bool)
	for _, chosen := range student.selected {
		for _, timeSlot := range e.sections[chosen.sectionID].Times {
			busy[timeSlot] = true
		}
	}
	student.busyTimes = busy
}

func (e *Engine) SelectedSection(studentID, courseID string) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.students[studentID] == nil {
		return "", false
	}
	chosen, ok := e.students[studentID].selected[courseID]
	if !ok {
		return "", false
	}
	return chosen.sectionID, true
}

func (e *Engine) SectionCount(sectionID string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.counts[sectionID]
}

func (e *Engine) SectionCapacity(sectionID string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sections[sectionID].Capacity
}
