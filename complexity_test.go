package enrollment

import (
	"fmt"
	"testing"
)

func TestComplexityIndependenceIsObservable(t *testing.T) {
	engine := NewEngine()
	mustConfigure(t, engine.AddCourse(Course{ID: "target", Credits: 1}))
	mustConfigure(t, engine.AddSection(Section{ID: "target-a", CourseID: "target", Capacity: 100000, Times: []int{1000000, 1000001}}))

	requests := make([]Request, 0, 10000)
	for index := 0; index < 10000; index++ {
		courseID := fmt.Sprintf("c%04d", index)
		sectionID := courseID + "-a"
		mustConfigure(t, engine.AddCourse(Course{ID: courseID, Credits: 1}))
		mustConfigure(t, engine.AddSection(Section{ID: sectionID, CourseID: courseID, Capacity: 1, Times: []int{index}}))
		requests = append(requests, Request{CourseID: courseID, SectionID: sectionID})
	}
	mustConfigure(t, engine.SetCreditLimit("s1", 100000))
	mustConfigure(t, engine.EnrollBatch("s1", requests))

	conflict, err := engine.HasTimeConflict("s1", "target-a")
	if err != nil || conflict {
		t.Fatalf("HasTimeConflict = (%v, %v), want (false, nil)", conflict, err)
	}
	available, err := engine.CapacityAvailable("target-a")
	if err != nil || !available {
		t.Fatalf("CapacityAvailable = (%v, %v), want (true, nil)", available, err)
	}
}

func BenchmarkHasTimeConflictOnlyScalesWithCandidateTimes(b *testing.B) {
	engine := NewEngine()
	mustConfigure(b, engine.AddCourse(Course{ID: "target", Credits: 1}))
	mustConfigure(b, engine.AddSection(Section{ID: "target-a", CourseID: "target", Capacity: 100000, Times: []int{1000000, 1000001}}))
	requests := make([]Request, 0, 10000)
	for index := 0; index < 10000; index++ {
		courseID := fmt.Sprintf("c%04d", index)
		sectionID := courseID + "-a"
		mustConfigure(b, engine.AddCourse(Course{ID: courseID, Credits: 1}))
		mustConfigure(b, engine.AddSection(Section{ID: sectionID, CourseID: courseID, Capacity: 1, Times: []int{index}}))
		requests = append(requests, Request{CourseID: courseID, SectionID: sectionID})
	}
	mustConfigure(b, engine.SetCreditLimit("s1", 100000))
	mustConfigure(b, engine.EnrollBatch("s1", requests))

	b.ResetTimer()
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		conflict, err := engine.HasTimeConflict("s1", "target-a")
		if err != nil || conflict {
			b.Fatalf("HasTimeConflict = (%v, %v), want (false, nil)", conflict, err)
		}
	}
}

func BenchmarkCapacityAvailableDoesNotScanEnrollments(b *testing.B) {
	engine := NewEngine()
	mustConfigure(b, engine.AddCourse(Course{ID: "target", Credits: 1}))
	mustConfigure(b, engine.AddSection(Section{ID: "target-a", CourseID: "target", Capacity: 100000, Times: []int{1000000}}))
	engine.mu.Lock()
	engine.counts["target-a"] = 9999
	engine.mu.Unlock()

	b.ResetTimer()
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		available, err := engine.CapacityAvailable("target-a")
		if err != nil || !available {
			b.Fatalf("CapacityAvailable = (%v, %v), want (true, nil)", available, err)
		}
	}
}
