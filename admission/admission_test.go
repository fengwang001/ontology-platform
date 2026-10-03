package admission

import (
	"errors"
	"reflect"
	"testing"
)

func pendingIDs(jobs []PendingJob) []string {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	return ids
}

func TestToleranceBoundary(t *testing.T) {
	controller := New()

	result := controller.Submit("exact", 0, 5, 3, 2, 10)
	t.Logf("input=Submit(exact,C=5,d=3,M=2) output=%+v basis=f-d=2 must equal M", result)
	if !result.Accepted {
		t.Fatalf("exact tolerance boundary rejected: %v", result.Reason)
	}

	result = controller.Submit("late", 0, 5, 3, 1, 10)
	t.Logf("input=Submit(late,C=5,d=3,M=1) output=%+v basis=now+C=5 exceeds d+M=4", result)
	if !errors.Is(result.Reason, ErrImpossible) {
		t.Fatalf("reason = %v, want ErrImpossible", result.Reason)
	}

	controller = New()
	controller.Submit("a", 0, 1, 1, 0, 10)
	result = controller.Submit("b", 0, 2, 2, 0, 1)
	t.Logf("input=Submit(b,C=2,d=2,M=0) after a(C=1,d=1) output=%+v basis=b alone can finish by 2, but EDF finish is 3 and f-d=1 is one too large", result)
	if result.Accepted || !errors.Is(result.Reason, ErrOverloaded) {
		t.Fatalf("result = %+v, want overload when f-d=M+1", result)
	}
}

func TestStartedJobCannotBeEvictedAndCandidateRollback(t *testing.T) {
	controller := New()
	controller.Submit("a", 0, 4, 6, 0, 8)
	controller.Submit("b", 0, 3, 8, 0, 3)

	result := controller.Submit("c", 1, 3, 5, 0, 9)
	t.Logf("input=Submit(c,now=1,C=3,d=5,v=9) output=%+v basis=a ran one unit and is unevictable; removing c restores b", result)
	if result.Accepted || !errors.Is(result.Reason, ErrOverloaded) {
		t.Fatalf("result = %+v, want overload rejection", result)
	}

	gotPending := controller.Pending()
	wantPending := []PendingJob{
		{ID: "a", Remaining: 4, Started: false},
		{ID: "b", Remaining: 3, Started: false},
	}
	if !reflect.DeepEqual(gotPending, wantPending) {
		t.Fatalf("pending = %+v, want %+v", gotPending, wantPending)
	}
	if controller.Value() != 0 || controller.clock != 0 || len(controller.Evicted()) != 0 {
		t.Fatalf("rejected submit changed state: value=%d clock=%d evicted=%v", controller.Value(), controller.clock, controller.Evicted())
	}
}

func TestEvictionByCrossProductAndTieID(t *testing.T) {
	controller := New()
	controller.Submit("a", 0, 1_000_000, 1_000_001, 0, 1_000_000)
	controller.Submit("b", 0, 999_999, 1_999_999, 0, 999_999)

	result := controller.Submit("c", 0, 1, 1, 0, 2)
	t.Logf("input=Submit(c,C=1,d=1,v=2) output=%+v basis=compare v1*r2 with v2*r1, no floating point and no int32 overflow", result)
	if !result.Accepted || !reflect.DeepEqual(result.Evicted, []string{"b"}) {
		t.Fatalf("result = %+v, want accepted and b evicted", result)
	}

	controller = New()
	controller.Submit("a", 0, 2, 3, 0, 4)
	controller.Submit("b", 0, 2, 4, 0, 4)
	result = controller.Submit("c", 0, 1, 1, 0, 10)
	t.Logf("input=tie density a=b output=%+v basis=equal v/r chooses lexicographically larger id b", result)
	if !result.Accepted || !reflect.DeepEqual(result.Evicted, []string{"b"}) {
		t.Fatalf("result = %+v, want b evicted first", result)
	}
}

func TestEvictMultipleJobsBeforeFeasible(t *testing.T) {
	controller := New()
	controller.Submit("a", 0, 3, 3, 0, 100)
	controller.Submit("x", 0, 2, 5, 0, 1)
	controller.Submit("y", 0, 2, 7, 0, 1)

	result := controller.Submit("c", 1, 3, 6, 0, 100)
	t.Logf("input=Submit(c,now=1,C=3,d=6,v=100) output=%+v basis=y and x both have lower density; equal-density tie evicts y then x", result)
	if !result.Accepted || !reflect.DeepEqual(result.Evicted, []string{"y", "x"}) {
		t.Fatalf("result = %+v, want evictions y,x", result)
	}
	want := []PendingJob{
		{ID: "a", Remaining: 2, Started: true},
		{ID: "c", Remaining: 3, Started: false},
	}
	if got := controller.Pending(); !reflect.DeepEqual(got, want) {
		t.Fatalf("pending = %+v, want %+v", got, want)
	}
	if controller.feasibility != 3 || controller.sortCount != 1 {
		t.Fatalf("sortCount=%d feasibility=%d, want 1 and 3", controller.sortCount, controller.feasibility)
	}
}

func TestDelayedValueFloorAndExactDeadline(t *testing.T) {
	controller := New()
	controller.Submit("p", 0, 5, 3, 3, 8)
	if err := controller.Advance(5); err != nil {
		t.Fatalf("Advance(5): %v", err)
	}
	t.Logf("input=Advance(5) output=value=%d basis=floor(8*(4-2)/4)=4", controller.Value())
	if controller.Value() != 4 {
		t.Fatalf("value = %d, want 4", controller.Value())
	}

	controller = New()
	controller.Submit("q", 0, 5, 5, 2, 8)
	if err := controller.Advance(5); err != nil {
		t.Fatalf("Advance(5): %v", err)
	}
	t.Logf("input=Advance(5) output=value=%d basis=finish exactly at d pays full v", controller.Value())
	if controller.Value() != 8 {
		t.Fatalf("value = %d, want 8", controller.Value())
	}
}

func TestIDReuseAndRejectedAdvanceIsNotPersisted(t *testing.T) {
	controller := New()
	controller.Submit("a", 0, 10, 10, 1, 7)

	result := controller.Submit("a", 5, 1, 6, 0, 1)
	t.Logf("input=duplicate Submit(a,now=5) output=%+v basis=a is still pending after provisional advance, so it is a duplicate", result)
	if !errors.Is(result.Reason, ErrDuplicateID) {
		t.Fatalf("result = %+v, want duplicate", result)
	}
	if controller.clock != 0 || controller.Value() != 0 {
		t.Fatalf("provisional advance persisted: clock=%d value=%d pending=%v", controller.clock, controller.Value(), controller.Pending())
	}
	if got := controller.Pending(); !reflect.DeepEqual(got, []PendingJob{{ID: "a", Remaining: 10, Started: false}}) {
		t.Fatalf("pending after duplicate rejection = %+v, want untouched job", got)
	}

	result = controller.Submit("b", 5, 1, 6, 0, 1)
	t.Logf("input=successful Submit(b,now=5) output=%+v basis=successful operation replays the discarded advance and starts a for five units", result)
	if !result.Accepted {
		t.Fatalf("reuse-time submission rejected: %v", result.Reason)
	}
	if got := controller.Pending(); !reflect.DeepEqual(got, []PendingJob{
		{ID: "b", Remaining: 1, Started: false},
		{ID: "a", Remaining: 5, Started: true},
	}) {
		t.Fatalf("pending = %+v, want b then started a", got)
	}
	if controller.Value() != 0 {
		t.Fatalf("value = %d, want 0 before a completes", controller.Value())
	}

	if err := controller.Advance(11); err != nil {
		t.Fatalf("Advance(11): %v", err)
	}
	if controller.Value() != 4 {
		t.Fatalf("value = %d, want b=1 plus floor(7*1/2)=3 for a", controller.Value())
	}

	result = controller.Submit("a", 11, 1, 12, 0, 1)
	t.Logf("input=reuse completed id a output=%+v basis=completed ids are available again", result)
	if !result.Accepted {
		t.Fatalf("completed id reuse rejected: %v", result.Reason)
	}
}

func TestPromptExampleEvictsLowerDensityJob(t *testing.T) {
	controller := New()
	first := controller.Submit("a", 0, 4, 6, 0, 8)
	second := controller.Submit("b", 0, 3, 8, 0, 3)
	third := controller.Submit("c", 1, 2, 5, 0, 5)
	t.Logf("input=a,b,c prompt sequence outputs=%+v,%+v,%+v basis=EDF c,a,b after one unit of a; b has density 1 and c has 2.5", first, second, third)
	if !third.Accepted || !reflect.DeepEqual(third.Evicted, []string{"b"}) {
		t.Fatalf("third = %+v, want accepted with b evicted", third)
	}

	if err := controller.Advance(10); err != nil {
		t.Fatalf("Advance(10): %v", err)
	}
	t.Logf("input=Advance(10) output=value=%d basis=c finishes at 3 for 5 and a finishes at 6 for 8", controller.Value())
	if controller.Value() != 13 {
		t.Fatalf("value = %d, want 13", controller.Value())
	}
}

func TestRejectionReasonOrder(t *testing.T) {
	controller := New()
	controller.Submit("a", 2, 1, 3, 0, 1)

	result := controller.Submit("", 1, 1, 0, 0, 1)
	t.Logf("input=invalid id with clock rollback output=%+v basis=invalid arguments is reported first", result)
	if !errors.Is(result.Reason, ErrInvalidArguments) {
		t.Fatalf("reason = %v, want invalid arguments", result.Reason)
	}

	result = controller.Submit("a", 1, 1, 0, 0, 1)
	t.Logf("input=duplicate id with clock rollback output=%+v basis=clock rollback precedes post-advance duplicate check", result)
	if !errors.Is(result.Reason, ErrClockRolledBack) {
		t.Fatalf("reason = %v, want clock rolled back", result.Reason)
	}
}
