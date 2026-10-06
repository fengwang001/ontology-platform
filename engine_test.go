package enrollment

import (
	"errors"
	"sync"
	"testing"
)

func mustConfigure(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("configuration failed: %v", err)
	}
}

func assertErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var rule *RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("error = %v, want code %s", err, want)
	}
	if rule.Code != want {
		t.Fatalf("error code = %s, want %s (%s)", rule.Code, want, rule.Message)
	}
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertSelected(t *testing.T, engine *Engine, studentID, courseID, sectionID string) {
	t.Helper()
	got, ok := engine.SelectedSection(studentID, courseID)
	if !ok || got != sectionID {
		t.Fatalf("course %s selected section = %q (%v), want %q", courseID, got, ok, sectionID)
	}
}

func assertNotSelected(t *testing.T, engine *Engine, studentID, courseID string) {
	t.Helper()
	if sectionID, ok := engine.SelectedSection(studentID, courseID); ok {
		t.Fatalf("course %s unexpectedly selected in section %s", courseID, sectionID)
	}
}

func newScenarioEngine(t *testing.T) *Engine {
	t.Helper()
	engine := NewEngine()
	mustConfigure(t, engine.AddCourse(Course{ID: "P", Credits: 2}))
	mustConfigure(t, engine.AddCourse(Course{ID: "C1", Credits: 3}))
	mustConfigure(t, engine.AddCourse(Course{ID: "C2", Credits: 3}))
	mustConfigure(t, engine.AddCourse(Course{ID: "M", Credits: 2}))
	mustConfigure(t, engine.AddCourse(Course{ID: "X", Credits: 2}))
	mustConfigure(t, engine.AddSection(Section{ID: "P-a", CourseID: "P", Capacity: 1, Times: []int{1}}))
	mustConfigure(t, engine.AddSection(Section{ID: "C1-a", CourseID: "C1", Capacity: 2, Times: []int{2}}))
	mustConfigure(t, engine.AddSection(Section{ID: "C2-a", CourseID: "C2", Capacity: 2, Times: []int{3}}))
	mustConfigure(t, engine.AddSection(Section{ID: "M-a", CourseID: "M", Capacity: 1, Times: []int{4}}))
	mustConfigure(t, engine.AddSection(Section{ID: "X-a", CourseID: "X", Capacity: 1, Times: []int{5}}))
	return engine
}

func TestBoundaryValuesAndBatchCorequisiteOrder(t *testing.T) {
	for _, requests := range [][]Request{
		{{CourseID: "P", SectionID: "P-a"}, {CourseID: "C1", SectionID: "C1-a"}, {CourseID: "C2", SectionID: "C2-a"}},
		{{CourseID: "C2", SectionID: "C2-a"}, {CourseID: "C1", SectionID: "C1-a"}, {CourseID: "P", SectionID: "P-a"}},
		{{CourseID: "C1", SectionID: "C1-a"}, {CourseID: "P", SectionID: "P-a"}, {CourseID: "C2", SectionID: "C2-a"}},
	} {
		t.Run("order", func(t *testing.T) {
			engine := newScenarioEngine(t)
			mustConfigure(t, engine.AddPrerequisite("C1", "P", 60))
			mustConfigure(t, engine.AddCorequisite("C1", "C2"))
			mustConfigure(t, engine.SetCreditLimit("s1", 8))
			mustConfigure(t, engine.SetHistory("s1", "P", 60, true))

			assertNoError(t, engine.EnrollBatch("s1", requests))
			assertSelected(t, engine, "s1", "P", "P-a")
			assertSelected(t, engine, "s1", "C1", "C1-a")
			assertSelected(t, engine, "s1", "C2", "C2-a")
		})
	}
}

func TestTwoLevelCascadeDrop(t *testing.T) {
	engine := newScenarioEngine(t)
	mustConfigure(t, engine.AddCourse(Course{ID: "C3", Credits: 2}))
	mustConfigure(t, engine.AddSection(Section{ID: "C3-a", CourseID: "C3", Capacity: 1, Times: []int{6}}))
	mustConfigure(t, engine.AddCorequisite("C1", "C2"))
	mustConfigure(t, engine.AddCorequisite("C2", "C3"))
	mustConfigure(t, engine.SetCreditLimit("s1", 8))
	assertNoError(t, engine.EnrollBatch("s1", []Request{
		{CourseID: "C1", SectionID: "C1-a"},
		{CourseID: "C2", SectionID: "C2-a"},
		{CourseID: "C3", SectionID: "C3-a"},
	}))

	assertNoError(t, engine.DropCourse("s1", "C1"))
	for _, courseID := range []string{"C1", "C2", "C3"} {
		assertNotSelected(t, engine, "s1", courseID)
	}
	if got := engine.SectionCount("C1-a"); got != 0 {
		t.Fatalf("C1-a count = %d, want 0", got)
	}
	if got := engine.SectionCount("C3-a"); got != 0 {
		t.Fatalf("C3-a count = %d, want 0", got)
	}
}

func TestCapacityLowerThenRejectDropAndRecover(t *testing.T) {
	engine := newScenarioEngine(t)
	mustConfigure(t, engine.SetCreditLimit("s1", 8))
	assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "C1", SectionID: "C1-a"}}))
	assertNoError(t, engine.SetCapacity("C1-a", 1))
	mustConfigure(t, engine.SetCreditLimit("s2", 8))
	assertErrorCode(t, engine.EnrollBatch("s2", []Request{{CourseID: "C1", SectionID: "C1-a"}}), ErrCapacityFull)
	if got := engine.SectionCount("C1-a"); got != 1 {
		t.Fatalf("count = %d, rejected enrollment must not mutate state", got)
	}

	assertNoError(t, engine.DropCourse("s1", "C1"))
	assertNoError(t, engine.EnrollBatch("s2", []Request{{CourseID: "C1", SectionID: "C1-a"}}))
	assertSelected(t, engine, "s2", "C1", "C1-a")
}

func TestCascadeReleaseAndConcurrentEnroll(t *testing.T) {
	engine := newScenarioEngine(t)
	mustConfigure(t, engine.AddCorequisite("C1", "C2"))
	mustConfigure(t, engine.SetCreditLimit("s1", 6))
	mustConfigure(t, engine.SetCreditLimit("s2", 6))
	mustConfigure(t, engine.SetCreditLimit("s3", 6))
	assertNoError(t, engine.EnrollBatch("s1", []Request{
		{CourseID: "C1", SectionID: "C1-a"},
		{CourseID: "C2", SectionID: "C2-a"},
	}))
	assertNoError(t, engine.SetCapacity("C2-a", 1))

	started := make(chan struct{})
	readyA := make(chan struct{})
	readyB := make(chan struct{})
	var waiter sync.WaitGroup
	waiter.Add(2)
	results := make(chan error, 2)
	go func() {
		defer waiter.Done()
		readyA <- struct{}{}
		<-started
		results <- engine.EnrollBatch("s2", []Request{
			{CourseID: "C1", SectionID: "C1-a"},
			{CourseID: "C2", SectionID: "C2-a"},
		})
	}()
	go func() {
		defer waiter.Done()
		readyB <- struct{}{}
		<-started
		results <- engine.EnrollBatch("s3", []Request{
			{CourseID: "C1", SectionID: "C1-a"},
			{CourseID: "C2", SectionID: "C2-a"},
		})
	}()
	<-readyA
	<-readyB
	close(started)
	assertNoError(t, engine.DropCourse("s1", "C1"))
	waiter.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			assertErrorCode(t, err, ErrCapacityFull)
		}
	}
	if successes != 1 {
		t.Fatalf("successful waiting enrollments = %d, want exactly 1", successes)
	}
	if got := engine.SectionCount("C2-a"); got != 1 {
		t.Fatalf("C2-a count = %d, want 1", got)
	}
}

func TestSwitchToMutuallyExclusiveCourseIsAtomic(t *testing.T) {
	engine := newScenarioEngine(t)
	mustConfigure(t, engine.SetCreditLimit("s1", 8))
	assertNoError(t, engine.EnrollBatch("s1", []Request{
		{CourseID: "C1", SectionID: "C1-a"},
		{CourseID: "M", SectionID: "M-a"},
	}))
	mustConfigure(t, engine.AddMutualExclusion("M", "X"))

	err := engine.SwitchSection("s1", "C1-a", Request{CourseID: "X", SectionID: "X-a"})
	assertErrorCode(t, err, ErrMutualExclusion)
	assertSelected(t, engine, "s1", "C1", "C1-a")
	assertNotSelected(t, engine, "s1", "X")
	if got := engine.SectionCount("C1-a"); got != 1 || engine.SectionCount("X-a") != 0 {
		t.Fatalf("counts after failed switch = C1:%d X:%d", got, engine.SectionCount("X-a"))
	}
}

func TestDropUnknownCourseHasDistinctError(t *testing.T) {
	engine := newScenarioEngine(t)
	assertErrorCode(t, engine.DropCourse("s1", "C1"), ErrNotEnrolled)
}

func TestSwitchToSameSectionIsInvalid(t *testing.T) {
	engine := newScenarioEngine(t)
	mustConfigure(t, engine.SetCreditLimit("s1", 8))
	assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "C1", SectionID: "C1-a"}}))
	err := engine.SwitchSection("s1", "C1-a", Request{CourseID: "C1", SectionID: "C1-a"})
	assertErrorCode(t, err, ErrInvalidArgument)
	assertSelected(t, engine, "s1", "C1", "C1-a")
}

func TestPrerequisitesAreDirectOnly(t *testing.T) {
	engine := newScenarioEngine(t)
	mustConfigure(t, engine.AddCourse(Course{ID: "Advanced", Credits: 2}))
	mustConfigure(t, engine.AddSection(Section{ID: "advanced-a", CourseID: "Advanced", Capacity: 1, Times: []int{9}}))
	mustConfigure(t, engine.AddPrerequisite("Advanced", "C1", 60))
	mustConfigure(t, engine.AddPrerequisite("C1", "P", 60))
	mustConfigure(t, engine.SetCreditLimit("s1", 8))
	mustConfigure(t, engine.SetHistory("s1", "C1", 90, true))

	assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "Advanced", SectionID: "advanced-a"}}))
	assertSelected(t, engine, "s1", "Advanced", "advanced-a")
}

func TestBatchFirstFailureIndexAndReason(t *testing.T) {
	engine := newScenarioEngine(t)
	mustConfigure(t, engine.AddPrerequisite("C1", "P", 60))
	mustConfigure(t, engine.SetCreditLimit("s1", 8))
	assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "X", SectionID: "X-a"}}))

	err := engine.EnrollBatch("s1", []Request{
		{CourseID: "P", SectionID: "P-a"},
		{CourseID: "X", SectionID: "X-a"},
		{CourseID: "C1", SectionID: "C1-a"},
	})
	assertErrorCode(t, err, ErrAlreadyEnrolled)
	var rule *RuleError
	errors.As(err, &rule)
	if rule.Index != 1 {
		t.Fatalf("failure index = %d, want 1", rule.Index)
	}
	assertNotSelected(t, engine, "s1", "P")
}

func TestRejectionPriorityAdjacentPairs(t *testing.T) {
	tests := []struct {
		name     string
		want     ErrorCode
		setup    func(*Engine)
		requests []Request
	}{
		{
			name: "invalid before not found",
			want: ErrInvalidArgument,
			requests: []Request{
				{CourseID: "missing", SectionID: "missing-a"},
				{CourseID: "", SectionID: ""},
			},
		},
		{
			name: "not found before already enrolled",
			want: ErrSectionNotFound,
			setup: func(engine *Engine) {
				mustConfigure(t, engine.SetCreditLimit("s1", 8))
				assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "C1", SectionID: "C1-a"}}))
			},
			requests: []Request{{CourseID: "C1", SectionID: "missing-a"}},
		},
		{
			name: "already before mutex",
			want: ErrAlreadyEnrolled,
			setup: func(engine *Engine) {
				mustConfigure(t, engine.SetCreditLimit("s1", 8))
				assertNoError(t, engine.EnrollBatch("s1", []Request{
					{CourseID: "C1", SectionID: "C1-a"},
					{CourseID: "M", SectionID: "M-a"},
				}))
				mustConfigure(t, engine.AddMutualExclusion("C1", "M"))
			},
			requests: []Request{{CourseID: "C1", SectionID: "C1-a"}},
		},
		{
			name: "mutex before prerequisite",
			want: ErrMutualExclusion,
			setup: func(engine *Engine) {
				mustConfigure(t, engine.AddMutualExclusion("C1", "M"))
				mustConfigure(t, engine.AddPrerequisite("C1", "P", 60))
				mustConfigure(t, engine.SetCreditLimit("s1", 8))
				assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "M", SectionID: "M-a"}}))
			},
			requests: []Request{{CourseID: "C1", SectionID: "C1-a"}},
		},
		{
			name: "prerequisite before corequisite",
			want: ErrPrerequisite,
			setup: func(engine *Engine) {
				mustConfigure(t, engine.AddPrerequisite("C1", "P", 60))
				mustConfigure(t, engine.AddCorequisite("C1", "C2"))
				mustConfigure(t, engine.SetCreditLimit("s1", 8))
			},
			requests: []Request{{CourseID: "C1", SectionID: "C1-a"}},
		},
		{
			name: "corequisite before credit",
			want: ErrCorequisite,
			setup: func(engine *Engine) {
				mustConfigure(t, engine.AddCorequisite("C1", "C2"))
				mustConfigure(t, engine.SetCreditLimit("s1", 2))
				assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "P", SectionID: "P-a"}}))
			},
			requests: []Request{{CourseID: "C1", SectionID: "C1-a"}},
		},
		{
			name: "credit before time",
			want: ErrCreditLimit,
			setup: func(engine *Engine) {
				mustConfigure(t, engine.SetCreditLimit("s1", 2))
				assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "P", SectionID: "P-a"}}))
				mustConfigure(t, engine.AddSection(Section{ID: "C1-b", CourseID: "C1", Capacity: 2, Times: []int{1}}))
			},
			requests: []Request{{CourseID: "C1", SectionID: "C1-b"}},
		},
		{
			name: "time before capacity",
			want: ErrTimeConflict,
			setup: func(engine *Engine) {
				mustConfigure(t, engine.SetCreditLimit("s1", 8))
				mustConfigure(t, engine.SetCreditLimit("s2", 8))
				assertNoError(t, engine.EnrollBatch("s1", []Request{{CourseID: "P", SectionID: "P-a"}}))
				assertNoError(t, engine.EnrollBatch("s2", []Request{{CourseID: "C1", SectionID: "C1-a"}}))
				mustConfigure(t, engine.AddSection(Section{ID: "C1-b", CourseID: "C1", Capacity: 0, Times: []int{1}}))
			},
			requests: []Request{{CourseID: "C1", SectionID: "C1-b"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := newScenarioEngine(t)
			if tt.setup != nil {
				tt.setup(engine)
			}
			assertErrorCode(t, engine.EnrollBatch("s1", tt.requests), tt.want)
		})
	}
}
