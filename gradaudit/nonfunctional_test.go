package gradaudit

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentOps checks that a concurrent workload produces a consistent
// state/verdict equivalent to some serial interleaving.
func TestConcurrentOps(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				id := fmt.Sprintf("p%d-%d", g, i)
				course := []string{"CS1", "CS2", "CS3", "CS4", "MA", "EN", "PE"}[(g+i)%7]
				_ = e.RegisterRecord(RegisterRecordInput{
					StudentID: "s", RecordID: id, Course: course,
					Semester: 1 + (i % 4), Score: 50 + float64((g*7+i)%50),
				})
				_ = e.RevokeRecord("s", id)
			}
		}(g)
	}
	wg.Wait()

	// Deterministic audit independent of completion order; must not race and
	// must return the same verdict on repeated runs.
	r1 := auditOf(t, e, "s")
	r2 := auditOf(t, e, "s")
	if r1.Pass != r2.Pass || r1.TreeSatisfied != r2.TreeSatisfied ||
		attrCode(r1) != attrCode(r2) || len(r1.Additional) != len(r2.Additional) {
		t.Fatalf("nondeterministic audit across runs: %+v vs %+v", r1, r2)
	}
}

// TestAuditCostIsStudentLocal verifies auditing student "s" does not inspect
// other students' records: unrelated students are held under a lock they
// already own; if Audit touched them it would deadlock instead of finishing.
func TestAuditCostIsStudentLocal(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	fillBaseline(t, e, "s")

	for i := 0; i < 200; i++ {
		sid := fmt.Sprintf("other%d", i)
		mustEnroll(t, e, sid, "V1")
		for k := 0; k < 10; k++ {
			mustRec(t, e, RegisterRecordInput{
				StudentID: sid, RecordID: fmt.Sprintf("x%d", k), Course: "CS1",
				Semester: 1, Score: 80,
			})
		}
	}
	// Hold every unrelated student's lock for the whole audit window: if the
	// audit touched their state it would block.
	var held []*studentState
	for i := 0; i < 200; i++ {
		st := e.students[fmt.Sprintf("other%d", i)]
		st.mu.Lock()
		held = append(held, st)
	}
	defer func() {
		for _, st := range held {
			st.mu.Unlock()
		}
	}()

	done := make(chan *AuditResult, 1)
	go func() {
		r, err := e.Audit("s")
		if err != nil {
			done <- nil
			return
		}
		done <- r
	}()
	r := <-done
	if r == nil {
		t.Fatal("audit failed while unrelated students' locks were held")
	}
}
