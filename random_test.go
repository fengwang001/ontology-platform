package enrollment

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

type registrar interface {
	AddCourse(Course) error
	AddSection(Section) error
	AddPrerequisite(string, string, int) error
	AddCorequisite(string, string) error
	AddMutualExclusion(string, string) error
	SetCreditLimit(string, int) error
	SetHistory(string, string, int, bool) error
	SetCapacity(string, int) error
	EnrollBatch(string, []Request) error
	DropCourse(string, string) error
	SwitchSection(string, string, Request) error
	SelectedSection(string, string) (string, bool)
	SectionCount(string) int
	SectionCapacity(string) int
}

type randomWorld struct {
	courses  []string
	sections []string
	students []string
}

func configureRandomWorld(t *testing.T, engine registrar, random *rand.Rand) randomWorld {
	t.Helper()
	courseCount := 6
	sectionCount := 0
	courses := make([]string, 0, courseCount)
	sections := make([]string, 0, courseCount*2)
	for index := 0; index < courseCount; index++ {
		courseID := fmt.Sprintf("c%d", index)
		courses = append(courses, courseID)
		mustConfigure(t, engine.AddCourse(Course{ID: courseID, Credits: 1 + random.IntN(3)}))
		sectionCountForCourse := 1 + random.IntN(2)
		for sectionIndex := 0; sectionIndex < sectionCountForCourse; sectionIndex++ {
			sectionID := fmt.Sprintf("%s-s%d", courseID, sectionIndex)
			sections = append(sections, sectionID)
			sectionCount++
			mustConfigure(t, engine.AddSection(Section{
				ID:       sectionID,
				CourseID: courseID,
				Capacity: random.IntN(4),
				Times:    []int{random.IntN(8), 8 + random.IntN(8)},
			}))
		}
	}

	students := []string{"alice", "bob"}
	for _, studentID := range students {
		mustConfigure(t, engine.SetCreditLimit(studentID, 2+random.IntN(10)))
		for _, courseID := range courses {
			if random.IntN(3) == 0 {
				mustConfigure(t, engine.SetHistory(studentID, courseID, 50+random.IntN(40), random.IntN(5) != 0))
			}
		}
	}
	for index := 0; index < 4; index++ {
		first := courses[random.IntN(len(courses))]
		second := courses[random.IntN(len(courses))]
		if first != second {
			if random.IntN(3) == 0 {
				mustConfigure(t, engine.AddCorequisite(first, second))
			} else {
				mustConfigure(t, engine.AddMutualExclusion(first, second))
			}
		}
	}
	for _, courseID := range courses {
		if random.IntN(2) == 0 && len(courses) > 1 {
			required := courses[random.IntN(len(courses))]
			if required != courseID {
				mustConfigure(t, engine.AddPrerequisite(courseID, required, 60))
			}
		}
	}
	return randomWorld{courses: courses, sections: sections, students: students}
}

func sectionsForCourse(world randomWorld, courseID string) []string {
	prefix := courseID + "-s"
	result := []string{}
	for _, sectionID := range world.sections {
		if len(sectionID) > len(prefix) && sectionID[:len(prefix)] == prefix {
			result = append(result, sectionID)
		}
	}
	return result
}

func randomRequests(random *rand.Rand, world randomWorld) []Request {
	count := 1 + random.IntN(3)
	used := map[string]bool{}
	requests := make([]Request, 0, count)
	for range count {
		courseID := world.courses[random.IntN(len(world.courses))]
		if used[courseID] {
			continue
		}
		sections := sectionsForCourse(world, courseID)
		requests = append(requests, Request{CourseID: courseID, SectionID: sections[random.IntN(len(sections))]})
		used[courseID] = true
	}
	return requests
}

func errorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var rule *RuleError
	if !errorAs(err, &rule) {
		return ErrorCode(err.Error())
	}
	return rule.Code
}

func errorIndexOf(err error) int {
	var rule *RuleError
	if errorAs(err, &rule) {
		return rule.Index
	}
	return -1
}

func errorAs(err error, target **RuleError) bool {
	rule, ok := err.(*RuleError)
	if ok {
		*target = rule
	}
	return ok
}

func compareWorlds(t *testing.T, efficient, naive registrar, world randomWorld, step int) {
	t.Helper()
	for _, studentID := range world.students {
		for _, courseID := range world.courses {
			efficientSection, efficientOK := efficient.SelectedSection(studentID, courseID)
			naiveSection, naiveOK := naive.SelectedSection(studentID, courseID)
			if efficientSection != naiveSection || efficientOK != naiveOK {
				t.Fatalf("step %d state mismatch for %s/%s: efficient=(%s,%v) naive=(%s,%v)", step, studentID, courseID, efficientSection, efficientOK, naiveSection, naiveOK)
			}
		}
	}
	for _, sectionID := range world.sections {
		if efficient.SectionCount(sectionID) != naive.SectionCount(sectionID) {
			t.Fatalf("step %d section %s count mismatch: efficient=%d naive=%d", step, sectionID, efficient.SectionCount(sectionID), naive.SectionCount(sectionID))
		}
		if efficient.SectionCapacity(sectionID) != naive.SectionCapacity(sectionID) {
			t.Fatalf("step %d section %s capacity mismatch", step, sectionID)
		}
	}
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	for _, seed := range []uint64{1474, 2026, 42, 99, 7} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, seed+1))
			efficient := NewEngine()
			naive := NewNaiveEngine()
			world := configureRandomWorld(t, efficient, random)
			random = rand.New(rand.NewPCG(seed, seed+1))
			world = configureRandomWorld(t, naive, random)

			for step := 0; step < 180; step++ {
				studentID := world.students[random.IntN(len(world.students))]
				operation := random.IntN(6)
				var efficientErr, naiveErr error
				input := ""
				basis := "configured priority: invalid, missing, duplicate, mutex, prerequisite, corequisite, credit, time, capacity"

				switch operation {
				case 0:
					requests := randomRequests(random, world)
					input = fmt.Sprintf("EnrollBatch(%s, %#v)", studentID, requests)
					efficientErr = efficient.EnrollBatch(studentID, requests)
					naiveErr = naive.EnrollBatch(studentID, requests)
				case 1:
					courseID := world.courses[random.IntN(len(world.courses))]
					input = fmt.Sprintf("DropCourse(%s, %s)", studentID, courseID)
					efficientErr = efficient.DropCourse(studentID, courseID)
					naiveErr = naive.DropCourse(studentID, courseID)
				case 2:
					from := world.sections[random.IntN(len(world.sections))]
					request := randomRequests(random, world)[0]
					input = fmt.Sprintf("SwitchSection(%s, %s, %#v)", studentID, from, request)
					efficientErr = efficient.SwitchSection(studentID, from, request)
					naiveErr = naive.SwitchSection(studentID, from, request)
				case 3:
					sectionID := world.sections[random.IntN(len(world.sections))]
					capacity := random.IntN(4)
					input = fmt.Sprintf("SetCapacity(%s, %d)", sectionID, capacity)
					basis = "management operation only rejects invalid or missing sections"
					efficientErr = efficient.SetCapacity(sectionID, capacity)
					naiveErr = naive.SetCapacity(sectionID, capacity)
				case 4:
					requests := randomRequests(random, world)
					if len(requests) > 1 && random.IntN(2) == 0 {
						requests[1] = requests[0]
					}
					input = fmt.Sprintf("EnrollBatch(%s, %#v)", studentID, requests)
					efficientErr = efficient.EnrollBatch(studentID, requests)
					naiveErr = naive.EnrollBatch(studentID, requests)
				default:
					courseID := world.courses[random.IntN(len(world.courses))]
					sections := sectionsForCourse(world, courseID)
					request := Request{CourseID: courseID, SectionID: sections[random.IntN(len(sections))]}
					input = fmt.Sprintf("SwitchSection(%s, %s, %#v)", studentID, request.SectionID, request)
					efficientErr = efficient.SwitchSection(studentID, request.SectionID, request)
					naiveErr = naive.SwitchSection(studentID, request.SectionID, request)
				}

				t.Logf("seed=%d step=%d input=%s efficient=(%s,index=%d) naive=(%s,index=%d) basis=%s",
					seed, step, input, errorCodeOf(efficientErr), errorIndexOf(efficientErr),
					errorCodeOf(naiveErr), errorIndexOf(naiveErr), basis)
				if errorCodeOf(efficientErr) != errorCodeOf(naiveErr) || errorIndexOf(efficientErr) != errorIndexOf(naiveErr) {
					t.Fatalf("step %d result mismatch: efficient=%v naive=%v", step, efficientErr, naiveErr)
				}
				compareWorlds(t, efficient, naive, world, step)
			}
		})
	}
}
