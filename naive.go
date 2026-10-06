package enrollment

import (
	"sort"
	"sync"
)

type naivePrerequisite struct {
	courseID     string
	minimumScore int
}

type NaiveEngine struct {
	mu            sync.Mutex
	courses       map[string]Course
	sections      map[string]Section
	prerequisites map[string][]naivePrerequisite
	corequisites  map[string]map[string]bool
	exclusions    map[string]map[string]bool
	limits        map[string]int
	history       map[string]map[string]historyCourse
	selected      map[string]map[string]selection
}

func NewNaiveEngine() *NaiveEngine {
	return &NaiveEngine{
		courses:       map[string]Course{},
		sections:      map[string]Section{},
		prerequisites: map[string][]naivePrerequisite{},
		corequisites:  map[string]map[string]bool{},
		exclusions:    map[string]map[string]bool{},
		limits:        map[string]int{},
		history:       map[string]map[string]historyCourse{},
		selected:      map[string]map[string]selection{},
	}
}

func (n *NaiveEngine) AddCourse(course Course) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if course.ID == "" || course.Credits <= 0 {
		return ruleError(ErrInvalidArgument, "course id and positive credits are required")
	}
	n.courses[course.ID] = course
	return nil
}

func (n *NaiveEngine) AddSection(section Section) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if section.ID == "" || section.CourseID == "" || section.Capacity < 0 {
		return ruleError(ErrInvalidArgument, "section id, course id and non-negative capacity are required")
	}
	if _, ok := n.courses[section.CourseID]; !ok {
		return ruleError(ErrCourseNotFound, "section course does not exist")
	}
	seenTimes := map[int]bool{}
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
	n.sections[section.ID] = section
	return nil
}

func (n *NaiveEngine) AddPrerequisite(courseID, requiredCourseID string, minimumScore int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if courseID == "" || requiredCourseID == "" {
		return ruleError(ErrInvalidArgument, "course ids are required")
	}
	if _, ok := n.courses[courseID]; !ok {
		return ruleError(ErrCourseNotFound, "course does not exist")
	}
	if _, ok := n.courses[requiredCourseID]; !ok {
		return ruleError(ErrCourseNotFound, "prerequisite course does not exist")
	}
	for index := range n.prerequisites[courseID] {
		if n.prerequisites[courseID][index].courseID == requiredCourseID {
			n.prerequisites[courseID][index].minimumScore = minimumScore
			return nil
		}
	}
	n.prerequisites[courseID] = append(n.prerequisites[courseID], naivePrerequisite{courseID: requiredCourseID, minimumScore: minimumScore})
	return nil
}

func (n *NaiveEngine) AddCorequisite(firstCourseID, secondCourseID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.addRelation(n.corequisites, firstCourseID, secondCourseID)
}

func (n *NaiveEngine) AddMutualExclusion(firstCourseID, secondCourseID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.addRelation(n.exclusions, firstCourseID, secondCourseID)
}

func (n *NaiveEngine) addRelation(relations map[string]map[string]bool, firstCourseID, secondCourseID string) error {
	if firstCourseID == "" || secondCourseID == "" || firstCourseID == secondCourseID {
		return ruleError(ErrInvalidArgument, "two distinct course ids are required")
	}
	if _, ok := n.courses[firstCourseID]; !ok {
		return ruleError(ErrCourseNotFound, "first course does not exist")
	}
	if _, ok := n.courses[secondCourseID]; !ok {
		return ruleError(ErrCourseNotFound, "second course does not exist")
	}
	put := func(from, to string) {
		if relations[from] == nil {
			relations[from] = map[string]bool{}
		}
		relations[from][to] = true
	}
	put(firstCourseID, secondCourseID)
	put(secondCourseID, firstCourseID)
	return nil
}

func (n *NaiveEngine) SetCreditLimit(studentID string, limit int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if studentID == "" || limit < 0 {
		return ruleError(ErrInvalidArgument, "student id and non-negative credit limit are required")
	}
	n.limits[studentID] = limit
	return nil
}

func (n *NaiveEngine) SetHistory(studentID, courseID string, score int, passed bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if studentID == "" || courseID == "" {
		return ruleError(ErrInvalidArgument, "student id and course id are required")
	}
	if _, ok := n.courses[courseID]; !ok {
		return ruleError(ErrCourseNotFound, "history course does not exist")
	}
	if n.history[studentID] == nil {
		n.history[studentID] = map[string]historyCourse{}
	}
	n.history[studentID][courseID] = historyCourse{score: score, passed: passed}
	return nil
}

func (n *NaiveEngine) SetCapacity(sectionID string, capacity int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if sectionID == "" || capacity < 0 {
		return ruleError(ErrInvalidArgument, "section id and non-negative capacity are required")
	}
	section, ok := n.sections[sectionID]
	if !ok {
		return ruleError(ErrSectionNotFound, "section does not exist")
	}
	section.Capacity = capacity
	n.sections[sectionID] = section
	return nil
}

func (n *NaiveEngine) EnrollBatch(studentID string, requests []Request) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if studentID == "" {
		return ruleError(ErrInvalidArgument, "student id is required")
	}
	seen := map[string]bool{}
	for index, request := range requests {
		if request.CourseID == "" || request.SectionID == "" {
			return batchError(index, ErrInvalidArgument, "course id and section id are required")
		}
		if seen[request.CourseID] {
			return batchError(index, ErrInvalidArgument, "course appears twice in one batch")
		}
		seen[request.CourseID] = true
	}

	chosen := n.selectionCopy(studentID)
	pending := map[string]bool{}
	for _, request := range requests {
		pending[request.CourseID] = true
	}
	credits := n.credits(studentID)
	accepted := map[string]bool{}
	pendingSections := map[string]bool{}
	for index, request := range requests {
		course, section, err := n.lookup(request)
		if err != nil {
			return withBatchIndex(index, err)
		}
		if _, exists := chosen[request.CourseID]; exists {
			return batchError(index, ErrAlreadyEnrolled, "course is already selected")
		}
		if err := n.mutexError(studentID, request.CourseID, accepted, chosen); err != nil {
			return withBatchIndex(index, err)
		}
		if err := n.prerequisiteError(studentID, request.CourseID); err != nil {
			return withBatchIndex(index, err)
		}
		if err := n.corequisiteError(request.CourseID, pending, chosen); err != nil {
			return withBatchIndex(index, err)
		}
		credits += course.Credits
		if credits > n.limits[studentID] {
			return batchError(index, ErrCreditLimit, "student credit limit exceeded")
		}
		if n.timeConflict(section, chosen, "") || n.pendingTimeConflict(section, pendingSections) {
			return batchError(index, ErrTimeConflict, "section time conflicts with selection")
		}
		if pendingSections[request.SectionID] || n.sectionCount(request.SectionID) >= section.Capacity {
			return batchError(index, ErrCapacityFull, "section has no available capacity")
		}
		chosen[request.CourseID] = selection{courseID: request.CourseID, sectionID: request.SectionID}
		accepted[request.CourseID] = true
		pendingSections[request.SectionID] = true
	}
	n.selected[studentID] = chosen
	return nil
}

func (n *NaiveEngine) DropCourse(studentID, courseID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if studentID == "" || courseID == "" {
		return ruleError(ErrInvalidArgument, "student id and course id are required")
	}
	if _, ok := n.courses[courseID]; !ok {
		return ruleError(ErrCourseNotFound, "course does not exist")
	}
	chosen := n.selected[studentID]
	if chosen == nil {
		return ruleError(ErrNotEnrolled, "course is not selected")
	}
	if _, ok := chosen[courseID]; !ok {
		return ruleError(ErrNotEnrolled, "course is not selected")
	}
	dropped := map[string]bool{courseID: true}
	queue := []string{courseID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for related := range n.corequisites[current] {
			if _, ok := chosen[related]; ok && !dropped[related] {
				dropped[related] = true
				queue = append(queue, related)
			}
		}
	}
	for droppedCourseID := range dropped {
		delete(chosen, droppedCourseID)
	}
	return nil
}

func (n *NaiveEngine) SwitchSection(studentID string, fromSectionID string, to Request) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if studentID == "" || fromSectionID == "" || to.CourseID == "" || to.SectionID == "" {
		return ruleError(ErrInvalidArgument, "student id, old section and target request are required")
	}
	fromSection, ok := n.sections[fromSectionID]
	if !ok {
		return ruleError(ErrSectionNotFound, "old section does not exist")
	}
	_, targetSection, err := n.lookup(to)
	if err != nil {
		return err
	}
	if fromSectionID == to.SectionID {
		return ruleError(ErrInvalidArgument, "target section must differ from old section")
	}
	if n.selected[studentID] == nil {
		return ruleError(ErrNotEnrolled, "old section is not selected")
	}
	current := n.selected[studentID][fromSection.CourseID]
	if current.sectionID != fromSectionID {
		return ruleError(ErrNotEnrolled, "old section is not selected")
	}
	if _, exists := n.selected[studentID][to.CourseID]; exists && to.CourseID != fromSection.CourseID {
		return ruleError(ErrAlreadyEnrolled, "target course is already selected")
	}

	tentative := n.selectionCopy(studentID)
	delete(tentative, fromSection.CourseID)
	tentative[to.CourseID] = selection{courseID: to.CourseID, sectionID: to.SectionID}
	pending := map[string]bool{to.CourseID: true}
	if err := n.mutexError(studentID, to.CourseID, pending, tentative); err != nil {
		return err
	}
	if err := n.prerequisiteError(studentID, to.CourseID); err != nil {
		return err
	}
	for courseID := range tentative {
		if err := n.corequisiteError(courseID, pending, tentative); err != nil {
			return err
		}
	}
	credits := 0
	for courseID := range tentative {
		credits += n.courses[courseID].Credits
	}
	if credits > n.limits[studentID] {
		return ruleError(ErrCreditLimit, "student credit limit exceeded")
	}
	if n.timeConflict(targetSection, tentative, fromSectionID) {
		return ruleError(ErrTimeConflict, "target section time conflicts with selection")
	}
	if n.sectionCountWith(to.SectionID, fromSectionID) >= targetSection.Capacity {
		return ruleError(ErrCapacityFull, "target section has no available capacity")
	}
	n.selected[studentID] = tentative
	return nil
}

func (n *NaiveEngine) lookup(request Request) (Course, Section, error) {
	course, ok := n.courses[request.CourseID]
	if !ok {
		return Course{}, Section{}, ruleError(ErrCourseNotFound, "course does not exist")
	}
	section, ok := n.sections[request.SectionID]
	if !ok || section.CourseID != request.CourseID {
		return Course{}, Section{}, ruleError(ErrSectionNotFound, "teaching section does not exist for course")
	}
	return course, section, nil
}

func (n *NaiveEngine) selectionCopy(studentID string) map[string]selection {
	result := map[string]selection{}
	for courseID, chosen := range n.selected[studentID] {
		result[courseID] = chosen
	}
	return result
}

func (n *NaiveEngine) credits(studentID string) int {
	total := 0
	for courseID := range n.selected[studentID] {
		total += n.courses[courseID].Credits
	}
	return total
}

func (n *NaiveEngine) mutexError(studentID, courseID string, accepted map[string]bool, chosen map[string]selection) error {
	for excluded := range n.exclusions[courseID] {
		if _, ok := chosen[excluded]; ok || accepted[excluded] {
			return ruleError(ErrMutualExclusion, "course conflicts with a mutually exclusive course")
		}
		if history, ok := n.history[studentID][excluded]; ok && history.passed {
			return ruleError(ErrMutualExclusion, "course conflicts with a passed historical course")
		}
	}
	return nil
}

func (n *NaiveEngine) prerequisiteError(studentID, courseID string) error {
	for _, required := range n.prerequisites[courseID] {
		history := n.history[studentID][required.courseID]
		if !history.passed || history.score < required.minimumScore {
			return ruleError(ErrPrerequisite, "prerequisite is not satisfied")
		}
	}
	return nil
}

func (n *NaiveEngine) corequisiteError(courseID string, pending map[string]bool, chosen map[string]selection) error {
	for required := range n.corequisites[courseID] {
		if _, selected := chosen[required]; selected || pending[required] {
			continue
		}
		return ruleError(ErrCorequisite, "corequisite is not selected in the same batch")
	}
	return nil
}

func (n *NaiveEngine) timeConflict(section Section, chosen map[string]selection, removedSectionID string) bool {
	busy := map[int]bool{}
	for _, current := range chosen {
		if current.sectionID == removedSectionID {
			continue
		}
		for _, timeSlot := range n.sections[current.sectionID].Times {
			busy[timeSlot] = true
		}
	}
	for _, timeSlot := range section.Times {
		if busy[timeSlot] {
			return true
		}
	}
	return false
}

func (n *NaiveEngine) pendingTimeConflict(section Section, pendingSections map[string]bool) bool {
	busy := map[int]bool{}
	for sectionID := range pendingSections {
		for _, timeSlot := range n.sections[sectionID].Times {
			busy[timeSlot] = true
		}
	}
	for _, timeSlot := range section.Times {
		if busy[timeSlot] {
			return true
		}
	}
	return false
}

func (n *NaiveEngine) sectionCount(sectionID string) int {
	count := 0
	for _, chosen := range n.selected {
		for _, current := range chosen {
			if current.sectionID == sectionID {
				count++
			}
		}
	}
	return count
}

func (n *NaiveEngine) sectionCountWith(sectionID, removedSectionID string) int {
	count := n.sectionCount(sectionID)
	if sectionID != removedSectionID && n.sectionCount(removedSectionID) > 0 {
		count++
	}
	return count
}

func (n *NaiveEngine) SelectedSection(studentID, courseID string) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	chosen, ok := n.selected[studentID][courseID]
	return chosen.sectionID, ok
}

func (n *NaiveEngine) SectionCount(sectionID string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sectionCount(sectionID)
}

func (n *NaiveEngine) SectionCapacity(sectionID string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sections[sectionID].Capacity
}
