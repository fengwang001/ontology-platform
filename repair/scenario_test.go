package repair

import (
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{
		Limits: [4]Limits{
			{Response: 10, Completion: 30},
			{Response: 8, Completion: 25},
			{Response: 6, Completion: 20},
			{Response: 4, Completion: 12},
		},
		RejectLimit: 2,
	}
}

func mustWorker(t *testing.T, s *Service, now int, trades, buildings []string, capacity int, urgent bool) int64 {
	t.Helper()
	worker, err := s.RegisterContractor(now, trades, buildings, capacity, urgent)
	if err != nil {
		t.Fatalf("register contractor: %v", err)
	}
	return worker.ID
}

func mustSubmit(t *testing.T, s *Service, now int, tenant int64, trade, building string, level Level) int64 {
	t.Helper()
	work, err := s.Submit(now, tenant, trade, building, level)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return work.ID
}

func mustDispatch(t *testing.T, s *Service, now int) Ticket {
	t.Helper()
	work, err := s.DispatchNext(now)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	return work
}

func TestCandidateOrderLoadCompletionRegistration(t *testing.T) {
	s, _ := New(testConfig())
	a := mustWorker(t, s, 0, []string{"plumbing"}, []string{"A"}, 2, false)
	b := mustWorker(t, s, 0, []string{"plumbing"}, []string{"A"}, 2, false)
	c := mustWorker(t, s, 0, []string{"plumbing"}, []string{"A"}, 1, false)

	first := mustSubmit(t, s, 1, 1, "plumbing", "A", Level1)
	if got := mustDispatch(t, s, 2).Assignee; got != a {
		t.Fatalf("first assignee = %d, want %d", got, a)
	}
	if _, err := s.Confirm(3, first, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(4, first, a); err != nil {
		t.Fatal(err)
	}
	second := mustSubmit(t, s, 5, 2, "plumbing", "A", Level1)
	if got := mustDispatch(t, s, 6).Assignee; got != b {
		t.Fatalf("empty contractor tie = %d, want %d", got, b)
	}
	third := mustSubmit(t, s, 7, 3, "plumbing", "A", Level1)
	if got := mustDispatch(t, s, 8).Assignee; got != c {
		t.Fatalf("registration tie = %d, want %d", got, c)
	}
	fourth := mustSubmit(t, s, 9, 4, "plumbing", "A", Level1)
	if got := mustDispatch(t, s, 10).Assignee; got != a {
		t.Fatalf("load tie, earlier completion = %d, want %d", got, a)
	}
	if second == 0 || third == 0 || fourth == 0 {
		t.Fatal("ticket ids must be positive")
	}
}

func TestUrgentPreemptionVictimOrderAndConfirmedSafe(t *testing.T) {
	s, _ := New(testConfig())
	worker := mustWorker(t, s, 0, []string{"electric"}, []string{"B"}, 2, true)
	older := mustSubmit(t, s, 1, 1, "electric", "B", Level1)
	mustDispatch(t, s, 1)
	newer := mustSubmit(t, s, 2, 2, "electric", "B", Level2)
	mustDispatch(t, s, 2)
	if _, err := s.Confirm(3, newer, worker); err != nil {
		t.Fatal(err)
	}
	urgent := mustSubmit(t, s, 4, 3, "electric", "B", Urgent)
	got := mustDispatch(t, s, 4)
	if got.ID != urgent || got.Assignee != worker {
		t.Fatalf("urgent dispatch = %+v", got)
	}
	victim, err := s.TicketAt(4, older)
	if err != nil {
		t.Fatal(err)
	}
	if victim.Status != StatusQueued || victim.SubmittedAt != 1 || victim.Level != Level1 {
		t.Fatalf("older unconfirmed ticket = %+v", victim)
	}
	safe, _ := s.TicketAt(4, newer)
	if safe.Status != StatusConfirmed || safe.Assignee != worker {
		t.Fatalf("confirmed ticket was preempted: %+v", safe)
	}
}

func TestResponseDeadlineExactAndOneOver(t *testing.T) {
	s, _ := New(testConfig())
	worker := mustWorker(t, s, 0, []string{"lock"}, []string{"C"}, 1, true)
	work := mustSubmit(t, s, 0, 1, "lock", "C", Level1)
	mustDispatch(t, s, 0)
	confirmed, err := s.Confirm(10, work, worker)
	if err != nil || confirmed.Status != StatusConfirmed {
		t.Fatalf("exact deadline confirm: work=%+v err=%v", confirmed, err)
	}
	if _, err := s.Complete(10, work, worker); err != nil {
		t.Fatal(err)
	}

	second := mustSubmit(t, s, 11, 2, "lock", "C", Level1)
	mustDispatch(t, s, 11)
	view, err := s.TicketAt(22, second)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != StatusQueued || view.Rejections != 1 {
		t.Fatalf("one unit overdue = %+v", view)
	}
}

func TestRejectionUpgradeAtRAndUrgentStaysUrgent(t *testing.T) {
	s, _ := New(testConfig())
	first := mustWorker(t, s, 0, []string{"paint"}, []string{"D"}, 1, false)
	second := mustWorker(t, s, 0, []string{"paint"}, []string{"D"}, 1, false)
	work := mustSubmit(t, s, 0, 1, "paint", "D", Level1)
	mustDispatch(t, s, 0)
	if _, err := s.Reject(1, work, first); err != nil {
		t.Fatal(err)
	}
	again, err := s.DispatchNext(2)
	if err != nil || again.Assignee != second {
		t.Fatalf("second dispatch = %+v, %v", again, err)
	}
	upgraded, err := s.Reject(3, work, second)
	if err != nil || upgraded.Level != Level2 || upgraded.LevelStartedAt != 3 || upgraded.Rejections != 0 {
		t.Fatalf("upgrade = %+v, %v", upgraded, err)
	}
	after, _ := s.TicketAt(3, work)
	if after.ResponseDue != 0 {
		t.Fatalf("queued upgraded ticket should not retain response due: %+v", after)
	}
	mustWorker(t, s, 4, []string{"paint"}, []string{"D"}, 1, false)
	redispatched := mustDispatch(t, s, 4)
	if redispatched.Level != Level2 || redispatched.ResponseDue != 12 {
		t.Fatalf("new level deadline = %+v", redispatched)
	}

	urgentService, _ := New(Config{
		Limits:      testConfig().Limits,
		RejectLimit: 1,
	})
	uWorker, _ := urgentService.RegisterContractor(0, []string{"paint"}, []string{"D"}, 1, true)
	uWork := mustSubmit(t, urgentService, 0, 1, "paint", "D", Urgent)
	urgentService.DispatchNext(0)
	urgentView, err := urgentService.Reject(1, uWork, uWorker.ID)
	if err != nil || urgentView.Level != Urgent || urgentView.Rejections != 1 {
		t.Fatalf("urgent rejection = %+v, %v", urgentView, err)
	}
}

func TestDeactivationReturnsOnlyUnconfirmed(t *testing.T) {
	s, _ := New(testConfig())
	worker := mustWorker(t, s, 0, []string{"gas"}, []string{"E"}, 2, true)
	unconfirmed := mustSubmit(t, s, 0, 1, "gas", "E", Level1)
	mustDispatch(t, s, 0)
	confirmed := mustSubmit(t, s, 1, 2, "gas", "E", Level2)
	mustDispatch(t, s, 1)
	if _, err := s.Confirm(4, confirmed, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeactivateContractor(5, worker); err != nil {
		t.Fatal(err)
	}
	left, _ := s.TicketAt(5, unconfirmed)
	kept, _ := s.TicketAt(5, confirmed)
	if left.Status != StatusQueued || left.Assignee != 0 {
		t.Fatalf("unconfirmed = %+v", left)
	}
	if kept.Status != StatusConfirmed || kept.Assignee != worker {
		t.Fatalf("confirmed = %+v", kept)
	}
}

func TestPermissionsAndRejectedOperationLeavesNoTrace(t *testing.T) {
	s, _ := New(testConfig())
	worker := mustWorker(t, s, 0, []string{"door"}, []string{"F"}, 1, false)
	other := mustWorker(t, s, 0, []string{"door"}, []string{"G"}, 1, false)
	work := mustSubmit(t, s, 0, 10, "door", "F", Level1)
	mustDispatch(t, s, 0)
	before := len(s.Events())
	if _, err := s.Cancel(1, work, 11); !errors.Is(err, ErrPermission) {
		t.Fatalf("cancel permission = %v", err)
	}
	if _, err := s.Confirm(1, work, other); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("non-assignee confirm = %v", err)
	}
	if _, err := s.Complete(1, work, other); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("complete unconfirmed = %v", err)
	}
	view, _ := s.TicketAt(1, work)
	if view.Status != StatusAssigned || len(s.Events()) != before {
		t.Fatalf("rejected operation changed state: %+v events=%d", view, len(s.Events())-before)
	}
	if _, err := s.Confirm(2, work, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(3, work, 10); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("confirmed cancel = %v", err)
	}
	if _, err := s.Complete(4, work, other); !errors.Is(err, ErrPermission) {
		t.Fatalf("other complete permission = %v", err)
	}
	if _, err := s.Complete(5, work, worker); err != nil {
		t.Fatalf("owner complete: %v", err)
	}
	if _, err := s.Complete(6, work, worker); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("complete again = %v", err)
	}
}

func TestErrorPrecedenceAndClockBack(t *testing.T) {
	s, _ := New(testConfig())
	if _, err := s.Submit(-1, 0, "", "A", Level1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid arguments = %v", err)
	}
	mustSubmit(t, s, 5, 1, "x", "A", Level1)
	if _, err := s.TicketAt(4, 1); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock back = %v", err)
	}
	if _, err := s.TicketAt(4, 999); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock precedes missing = %v", err)
	}
}

func TestFailedDispatchLeavesNoOverdueSideEffect(t *testing.T) {
	s, _ := New(testConfig())
	worker := mustWorker(t, s, 0, []string{"fire"}, []string{"X"}, 1, true)
	work := mustSubmit(t, s, 0, 1, "fire", "X", Level1)
	mustDispatch(t, s, 0)
	before := len(s.Events())
	if _, err := s.DispatchNext(11); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("dispatch no candidate = %v", err)
	}
	view, err := s.TicketAt(10, work)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != StatusAssigned || view.Assignee != worker || len(s.Events()) != before {
		t.Fatalf("failed dispatch changed state: %+v events=%d", view, len(s.Events())-before)
	}
}

func TestConcurrentDispatchRespectsCapacityAndAssignmentUniqueness(t *testing.T) {
	s, _ := New(testConfig())
	mustWorker(t, s, 0, []string{"hvac"}, []string{"H"}, 2, true)
	for i := 1; i <= 6; i++ {
		if _, err := s.Submit(0, int64(i), "hvac", "H", Level1); err != nil {
			t.Fatal(err)
		}
	}
	ready := make(chan struct{})
	done := make(chan struct{}, 8)
	for i := 0; i < 8; i++ {
		go func() {
			<-ready
			_, _ = s.DispatchAll(0)
			done <- struct{}{}
		}()
	}
	close(ready)
	for i := 0; i < 8; i++ {
		<-done
	}
	assigned := 0
	for id := int64(1); id <= 6; id++ {
		work, err := s.TicketAt(0, id)
		if err != nil {
			t.Fatal(err)
		}
		if work.Status == StatusAssigned {
			assigned++
			if work.Assignee != 1 {
				t.Fatalf("ticket %d assigned to %d", id, work.Assignee)
			}
		}
	}
	if assigned != 2 {
		t.Fatalf("assigned = %d, want 2", assigned)
	}
}
