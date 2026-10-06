package enrollment

import "sort"

func (e *Engine) AddCourse(course Course) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if course.ID == "" || course.Credits <= 0 {
		return ruleError(ErrInvalidArgument, "course id and positive credits are required")
	}
	e.courses[course.ID] = course
	return nil
}

func (e *Engine) AddSection(section Section) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if section.ID == "" || section.CourseID == "" || section.Capacity < 0 {
		return ruleError(ErrInvalidArgument, "section id, course id and non-negative capacity are required")
	}
	if _, ok := e.courses[section.CourseID]; !ok {
		return ruleError(ErrCourseNotFound, "section course does not exist")
	}
	seenTimes := make(map[int]bool, len(section.Times))
	times := make([]int, 0, len(section.Times))
	for _, timeSlot := range section.Times {
		if timeSlot < 0 || seenTimes[timeSlot] {
			continue
		}
		seenTimes[timeSlot] = true
		times = append(times, timeSlot)
	}
	sort.Ints(times)
	section.Times = times

	if old, ok := e.sections[section.ID]; ok && old.CourseID != section.CourseID {
		return ruleError(ErrInvalidArgument, "section belongs to another course")
	}
	e.sections[section.ID] = section
	sections := e.courseSection[section.CourseID]
	if sections == nil {
		sections = make(map[string]Section)
		e.courseSection[section.CourseID] = sections
	}
	sections[section.ID] = section
	if _, exists := e.counts[section.ID]; !exists {
		e.counts[section.ID] = 0
	}
	return nil
}

func (e *Engine) AddPrerequisite(courseID, requiredCourseID string, minimumScore int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if courseID == "" || requiredCourseID == "" {
		return ruleError(ErrInvalidArgument, "course ids are required")
	}
	if _, ok := e.courses[courseID]; !ok {
		return ruleError(ErrCourseNotFound, "course does not exist")
	}
	if _, ok := e.courses[requiredCourseID]; !ok {
		return ruleError(ErrCourseNotFound, "prerequisite course does not exist")
	}
	for index := range e.prerequisites[courseID] {
		if e.prerequisites[courseID][index].courseID == requiredCourseID {
			e.prerequisites[courseID][index].minimumScore = minimumScore
			return nil
		}
	}
	e.prerequisites[courseID] = append(e.prerequisites[courseID], prerequisite{
		courseID:     requiredCourseID,
		minimumScore: minimumScore,
	})
	return nil
}

func (e *Engine) AddCorequisite(firstCourseID, secondCourseID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.addUndirectedCourseRelation(e.corequisites, firstCourseID, secondCourseID)
}

func (e *Engine) AddMutualExclusion(firstCourseID, secondCourseID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.addUndirectedCourseRelation(e.exclusions, firstCourseID, secondCourseID)
}

func (e *Engine) addUndirectedCourseRelation(relations map[string]map[string]bool, firstCourseID, secondCourseID string) error {
	if firstCourseID == "" || secondCourseID == "" || firstCourseID == secondCourseID {
		return ruleError(ErrInvalidArgument, "two distinct course ids are required")
	}
	if _, ok := e.courses[firstCourseID]; !ok {
		return ruleError(ErrCourseNotFound, "first course does not exist")
	}
	if _, ok := e.courses[secondCourseID]; !ok {
		return ruleError(ErrCourseNotFound, "second course does not exist")
	}
	addRelation := func(from, to string) {
		related := relations[from]
		if related == nil {
			related = make(map[string]bool)
			relations[from] = related
		}
		related[to] = true
	}
	addRelation(firstCourseID, secondCourseID)
	addRelation(secondCourseID, firstCourseID)
	return nil
}
