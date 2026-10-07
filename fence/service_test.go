package fence

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Timezone:                "Asia/Shanghai",
		OutsideFee:              5,
		RewardAmount:            3,
		DefaultOperatingFenceID: "O1",
		EvacuationNum:           3,
		EvacuationDen:           4,
		ClaimTimeoutSec:         30,
	}
}

func newService(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(testConfig())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func rect(id string, typ FenceType, x0, y0, x1, y1 int64, cap int) Fence {
	return Fence{
		ID:   id,
		Type: typ,
		Vertices: []Point{
			{X: x0, Y: y0},
			{X: x1, Y: y0},
			{X: x1, Y: y1},
			{X: x0, Y: y1},
		},
		Capacity: cap,
	}
}

func mustRegister(t *testing.T, s *Service, f Fence, ts int64) {
	t.Helper()
	if err := s.RegisterFence(f, ts); err != nil {
		t.Fatalf("RegisterFence %s: %v", f.ID, err)
	}
}

func mustAddVehicle(t *testing.T, s *Service, id string, ts int64) {
	t.Helper()
	if err := s.AddVehicle(id, ts); err != nil {
		t.Fatalf("AddVehicle %s: %v", id, err)
	}
}

func mustUnlock(t *testing.T, s *Service, id string, ts int64) {
	t.Helper()
	if err := s.Unlock(id, ts); err != nil {
		t.Fatalf("Unlock %s: %v", id, err)
	}
}

// ride registers a vehicle and unlocks it, ready for a return.
func ride(t *testing.T, s *Service, id string, ts int64) {
	t.Helper()
	mustAddVehicle(t, s, id, ts)
	mustUnlock(t, s, id, ts)
}

func TestBoundaryAndVertexAttribution(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 10, 10, 2), 1)

	cases := []struct {
		p    Point
		want Attribution
	}{
		{Point{0, 0}, Attribution{Operating, "O1"}},   // vertex
		{Point{10, 10}, Attribution{Operating, "O1"}}, // vertex
		{Point{5, 0}, Attribution{Operating, "O1"}},   // edge midpoint
		{Point{0, 7}, Attribution{Operating, "O1"}},   // edge
		{Point{5, 5}, Attribution{Operating, "O1"}},   // interior
		{Point{-1, 5}, Attribution{Outside, ""}},      // outside
		{Point{11, 10}, Attribution{Outside, ""}},     // just past vertex
	}
	for _, c := range cases {
		if got := s.Locate(c.p); got != c.want {
			t.Errorf("Locate(%v) = %+v, want %+v", c.p, got, c.want)
		}
	}
}

func TestRewardTouchingOperatingBoundary(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 10, 10, 10), 1)
	// Shares the corner (0,0) and two edges with O1: allowed for nesting.
	mustRegister(t, s, rect("R1", Reward, 0, 0, 5, 5, 2), 2)

	if got := s.Locate(Point{0, 0}); got != (Attribution{Reward, "R1"}) {
		t.Fatalf("shared corner should attribute to reward fence, got %+v", got)
	}
	if got := s.Locate(Point{5, 2}); got != (Attribution{Reward, "R1"}) {
		t.Fatalf("reward edge point should attribute to reward fence, got %+v", got)
	}
	ride(t, s, "v1", 3)
	res, err := s.Return("v1", "u1", Point{0, 0}, 4)
	if err != nil {
		t.Fatalf("Return on shared boundary: %v", err)
	}
	if res.FenceID != "R1" || res.Reward != 3 {
		t.Fatalf("got %+v, want fence R1 with reward 3", res)
	}
}

func TestFenceConstraintRejections(t *testing.T) {
	newBase := func(t *testing.T) *Service {
		s := newService(t)
		mustRegister(t, s, rect("O1", Operating, 0, 0, 10, 10, 10), 1)
		mustRegister(t, s, rect("R1", Reward, 1, 1, 3, 3, 2), 2)
		return s
	}
	bowtie := []Point{{0, 0}, {4, 4}, {4, 0}, {0, 4}}

	t.Run("operating overlaps operating", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(rect("O2", Operating, 5, 5, 20, 20, 5), 3)
		if got := errCode(t, err); got != ErrFenceConstraint {
			t.Fatalf("got %v, want %v", got, ErrFenceConstraint)
		}
	})
	t.Run("operating nested in operating", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(rect("O2", Operating, 4, 4, 6, 6, 5), 3)
		if got := errCode(t, err); got != ErrFenceConstraint {
			t.Fatalf("got %v, want %v", got, ErrFenceConstraint)
		}
	})
	t.Run("operating touching operating", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(rect("O2", Operating, 10, 0, 20, 10, 5), 3)
		if got := errCode(t, err); got != ErrFenceConstraint {
			t.Fatalf("got %v, want %v", got, ErrFenceConstraint)
		}
	})
	t.Run("inner outside any operating fence", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(rect("R2", Reward, 20, 20, 25, 25, 2), 3)
		if got := errCode(t, err); got != ErrFenceConstraint {
			t.Fatalf("got %v, want %v", got, ErrFenceConstraint)
		}
	})
	t.Run("inner partially overlapping operating edge", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(rect("R2", Reward, 8, 8, 12, 12, 2), 3)
		if got := errCode(t, err); got != ErrFenceConstraint {
			t.Fatalf("got %v, want %v", got, ErrFenceConstraint)
		}
	})
	t.Run("reward overlaps reward", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(rect("R2", Reward, 2, 2, 4, 4, 2), 3)
		if got := errCode(t, err); got != ErrFenceConstraint {
			t.Fatalf("got %v, want %v", got, ErrFenceConstraint)
		}
	})
	t.Run("nopark overlaps reward", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(rect("N1", NoParking, 2, 2, 5, 5, 2), 3)
		if got := errCode(t, err); got != ErrFenceConstraint {
			t.Fatalf("got %v, want %v", got, ErrFenceConstraint)
		}
	})
	t.Run("self intersecting polygon", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(Fence{ID: "O2", Type: Operating, Vertices: bowtie, Capacity: 1}, 3)
		if got := errCode(t, err); got != ErrInvalidArgument {
			t.Fatalf("got %v, want %v", got, ErrInvalidArgument)
		}
	})
	t.Run("too few vertices", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(Fence{ID: "O2", Type: Operating,
			Vertices: []Point{{0, 0}, {1, 1}}, Capacity: 1}, 3)
		if got := errCode(t, err); got != ErrInvalidArgument {
			t.Fatalf("got %v, want %v", got, ErrInvalidArgument)
		}
	})
	t.Run("adjacent duplicate vertices", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(Fence{ID: "O2", Type: Operating,
			Vertices: []Point{{0, 0}, {0, 0}, {2, 2}}, Capacity: 1}, 3)
		if got := errCode(t, err); got != ErrInvalidArgument {
			t.Fatalf("got %v, want %v", got, ErrInvalidArgument)
		}
	})
	t.Run("duplicate fence ID", func(t *testing.T) {
		s := newBase(t)
		err := s.RegisterFence(rect("O1", Operating, 20, 20, 30, 30, 1), 3)
		if got := errCode(t, err); got != ErrInvalidArgument {
			t.Fatalf("got %v, want %v", got, ErrInvalidArgument)
		}
	})
	t.Run("operating containing existing inner fence is allowed", func(t *testing.T) {
		s := newService(t)
		mustRegister(t, s, rect("O1", Operating, 0, 0, 10, 10, 10), 1)
		mustRegister(t, s, rect("R1", Reward, 1, 1, 3, 3, 2), 2)
		// O2 fully contains R1 but does not touch O1: geometrically
		// impossible without conflicting with O1, so use a fence that
		// contains R1 together with O1 -> must be rejected.
		err := s.RegisterFence(rect("O2", Operating, -5, -5, 20, 20, 5), 3)
		if got := errCode(t, err); got != ErrFenceConstraint {
			t.Fatalf("got %v, want %v", got, ErrFenceConstraint)
		}
	})
}

func TestConcurrentReturnIntoFullFence(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 10, 10, 1), 1)

	const n = 8
	for i := 0; i < n; i++ {
		ride(t, s, fmt.Sprintf("v%d", i), int64(10+i))
	}
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Return(fmt.Sprintf("v%d", i), "u1", Point{5, 5}, 100)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	succeeded, full := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		if e, ok := err.(*Error); ok && e.Code == ErrFenceFull {
			full++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || full != n-1 {
		t.Fatalf("succeeded=%d full=%d, want 1 and %d", succeeded, full, n-1)
	}
	if c, _ := s.Count("O1"); c != 1 {
		t.Fatalf("count = %d, want 1", c)
	}
}

func TestRewardDailyLimit(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 10), 1)
	mustRegister(t, s, rect("R1", Reward, 0, 0, 10, 10, 10), 2)
	mustRegister(t, s, rect("R2", Reward, 20, 0, 30, 10, 10), 3)

	loc, _ := time.LoadLocation("Asia/Shanghai")
	day1 := time.Date(2026, 10, 6, 12, 0, 0, 0, loc).Unix()
	day1later := time.Date(2026, 10, 6, 23, 30, 0, 0, loc).Unix()
	day2 := time.Date(2026, 10, 7, 0, 30, 0, 0, loc).Unix()

	// First reward-zone return of the day: reward granted.
	ride(t, s, "v1", day1)
	res, err := s.Return("v1", "u1", Point{5, 5}, day1+1)
	if err != nil || res.Reward != 3 {
		t.Fatalf("first return: res=%+v err=%v, want reward 3", res, err)
	}
	// Same user, same fence, same natural day: no second reward.
	mustUnlock(t, s, "v1", day1+2)
	res, err = s.Return("v1", "u1", Point{5, 5}, day1later)
	if err != nil || res.Reward != 0 {
		t.Fatalf("same-day return: res=%+v err=%v, want reward 0", res, err)
	}
	// Same user, different reward fence, same day: independent limit.
	mustUnlock(t, s, "v1", day1later+1)
	res, err = s.Return("v1", "u1", Point{25, 5}, day1later+2)
	if err != nil || res.FenceID != "R2" || res.Reward != 3 {
		t.Fatalf("other fence return: res=%+v err=%v, want R2 reward 3", res, err)
	}
	// Different user, same fence, same day: independent limit.
	ride(t, s, "v2", day1later+3)
	res, err = s.Return("v2", "u2", Point{5, 5}, day1later+4)
	if err != nil || res.Reward != 3 {
		t.Fatalf("other user return: res=%+v err=%v, want reward 3", res, err)
	}
	// Next natural day (timezone-aware): the limit resets.
	mustUnlock(t, s, "v1", day2)
	res, err = s.Return("v1", "u1", Point{5, 5}, day2+1)
	if err != nil || res.FenceID != "R1" || res.Reward != 3 {
		t.Fatalf("next-day return: res=%+v err=%v, want R1 reward 3", res, err)
	}
	if n := len(s.Ledger()); n != 4 {
		t.Fatalf("ledger entries = %d, want 4 rewards", n)
	}
}

func TestThresholdExactTrigger(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 4), 1)
	mustRegister(t, s, rect("R1", Reward, 0, 0, 10, 10, 10), 2)

	// Ratio 3/4 over capacity 4: exactly 3 vehicles trigger, 2 do not.
	p := Point{50, 50}
	for i, wantTask := range []bool{false, false, true} {
		ride(t, s, fmt.Sprintf("v%d", i), int64(10+i*2))
		res, err := s.Return(fmt.Sprintf("v%d", i), "u1", p, int64(11+i*2))
		if err != nil {
			t.Fatalf("return %d: %v", i, err)
		}
		if gotTask := len(res.TaskIDs) == 1; gotTask != wantTask {
			t.Fatalf("return %d: tasks=%v, wantTask=%v", i, res.TaskIDs, wantTask)
		}
	}
	tasks := s.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Kind != EvacuateTask || task.SourceFenceID != "O1" ||
		task.DestFenceID != "R1" || task.MoveCount != 1 || task.Status != TaskPending {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestNoDuplicateEvacTaskWhileUnfinished(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 4), 1)
	mustRegister(t, s, rect("R1", Reward, 0, 0, 10, 10, 10), 2)

	p := Point{50, 50}
	ts := int64(10)
	returnIn := func(id string) ReturnResult {
		t.Helper()
		ride(t, s, id, ts)
		ts++
		res, err := s.Return(id, "u1", p, ts)
		ts++
		if err != nil {
			t.Fatalf("return %s: %v", id, err)
		}
		return res
	}
	returnIn("v1")
	returnIn("v2")
	if res := returnIn("v3"); len(res.TaskIDs) != 1 {
		t.Fatalf("third return should trigger, got %v", res.TaskIDs)
	}
	// Unfinished evacuation task exists: no duplicates even above threshold.
	if res := returnIn("v4"); len(res.TaskIDs) != 0 {
		t.Fatalf("fourth return must not re-trigger, got %v", res.TaskIDs)
	}
	if n := len(s.Tasks()); n != 1 {
		t.Fatalf("tasks = %d, want 1", n)
	}

	// Finish the task: one vehicle moves O1 -> R1.
	if err := s.ClaimTask("T1", "d1", ts); err != nil {
		t.Fatalf("claim: %v", err)
	}
	ts++
	cres, err := s.CompleteTask("T1", Point{5, 5}, ts)
	ts++
	if err != nil || cres.Moved != 1 || cres.FenceID != "R1" {
		t.Fatalf("complete: res=%+v err=%v", cres, err)
	}
	if c, _ := s.Count("O1"); c != 3 {
		t.Fatalf("O1 count = %d, want 3", c)
	}
	if c, _ := s.Count("R1"); c != 1 {
		t.Fatalf("R1 count = %d, want 1", c)
	}

	// The fence is at threshold again after another return: a fresh task
	// may now be generated because the previous one completed.
	if res := returnIn("v5"); len(res.TaskIDs) != 1 {
		t.Fatalf("return after completion should trigger again, got %v", res.TaskIDs)
	}
	if n := len(s.Tasks()); n != 2 {
		t.Fatalf("tasks = %d, want 2", n)
	}
}

// makeRecallTask registers O1, returns one vehicle outside and returns the
// created recall task ID.
func makeRecallTask(t *testing.T, s *Service, ts int64) (taskID string, next int64) {
	t.Helper()
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 10), ts)
	ts++
	ride(t, s, "v-out", ts)
	ts++
	res, err := s.Return("v-out", "u1", Point{500, 500}, ts)
	ts++
	if err != nil || !res.Outside || len(res.TaskIDs) != 1 {
		t.Fatalf("outside return: res=%+v err=%v", res, err)
	}
	return res.TaskIDs[0], ts
}

func TestClaimTimeoutExactBoundary(t *testing.T) {
	s := newService(t)
	taskID, ts := makeRecallTask(t, s, 1000)

	// Claim at T; timeout is 30s. One second before the limit the task is
	// still claimed; exactly at the limit the claim has expired.
	claimAt := ts
	if err := s.ClaimTask(taskID, "d1", claimAt); err != nil {
		t.Fatalf("claim: %v", err)
	}
	err := s.ClaimTask(taskID, "d2", claimAt+29)
	if got := errCode(t, err); got != ErrTaskAlreadyClaimed {
		t.Fatalf("at T+29 got %v, want %v", got, ErrTaskAlreadyClaimed)
	}
	if err := s.ClaimTask(taskID, "d2", claimAt+30); err != nil {
		t.Fatalf("at T+30 the claim should have expired: %v", err)
	}
	task, _ := s.Task(taskID)
	if task.Status != TaskInProgress || len(task.Claims) != 2 {
		t.Fatalf("task after re-claim: %+v", task)
	}
	if !task.Claims[0].Expired || task.Claims[0].DispatcherID != "d1" {
		t.Fatalf("original claim record must be kept and expired: %+v", task.Claims[0])
	}
	if task.Claims[1].Expired || task.Claims[1].DispatcherID != "d2" {
		t.Fatalf("second claim record: %+v", task.Claims[1])
	}

	// A completion attempted exactly at the timeout boundary finds the
	// task back in pending state.
	s2 := newService(t)
	task2, ts2 := makeRecallTask(t, s2, 1000)
	if err := s2.ClaimTask(task2, "d1", ts2); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = s2.CompleteTask(task2, Point{50, 50}, ts2+30)
	if got := errCode(t, err); got != ErrTaskNotInProgress {
		t.Fatalf("complete at exact timeout got %v, want %v", got, ErrTaskNotInProgress)
	}
}

func TestCompleteFullDropRejectedTaskStaysInProgress(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 1), 1)
	mustRegister(t, s, rect("R1", Reward, 0, 0, 10, 10, 2), 2)

	// Fill O1 to capacity.
	ride(t, s, "v1", 10)
	if _, err := s.Return("v1", "u1", Point{50, 50}, 11); err != nil {
		t.Fatalf("fill O1: %v", err)
	}
	// Outside return creates a recall task.
	ride(t, s, "v2", 12)
	res, err := s.Return("v2", "u1", Point{500, 500}, 13)
	if err != nil || len(res.TaskIDs) != 1 {
		t.Fatalf("outside return: res=%+v err=%v", res, err)
	}
	taskID := res.TaskIDs[0]
	if err := s.ClaimTask(taskID, "d1", 14); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Drop into the full operating fence: rejected, task stays in progress.
	_, err = s.CompleteTask(taskID, Point{50, 50}, 15)
	if got := errCode(t, err); got != ErrFenceFull {
		t.Fatalf("got %v, want %v", got, ErrFenceFull)
	}
	task, _ := s.Task(taskID)
	if task.Status != TaskInProgress {
		t.Fatalf("task status = %v, want %v", task.Status, TaskInProgress)
	}
	// Drop into the non-full reward fence succeeds.
	cres, err := s.CompleteTask(taskID, Point{5, 5}, 16)
	if err != nil || cres.Moved != 1 || cres.FenceID != "R1" {
		t.Fatalf("complete: res=%+v err=%v", cres, err)
	}
	if c, _ := s.Count("R1"); c != 1 {
		t.Fatalf("R1 count = %d, want 1", c)
	}
}

func TestCompleteDropNoParkingOrOutside(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 10), 1)
	mustRegister(t, s, rect("N1", NoParking, 10, 10, 20, 20, 5), 2)

	ride(t, s, "v1", 3)
	res, err := s.Return("v1", "u1", Point{500, 500}, 4)
	if err != nil || len(res.TaskIDs) != 1 {
		t.Fatalf("outside return: res=%+v err=%v", res, err)
	}
	taskID := res.TaskIDs[0]
	if err := s.ClaimTask(taskID, "d1", 5); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.CompleteTask(taskID, Point{15, 15}, 6); errCode(t, err) != ErrInvalidDropPoint {
		t.Fatalf("drop into no-parking should be %v", ErrInvalidDropPoint)
	}
	if _, err := s.CompleteTask(taskID, Point{500, 500}, 7); errCode(t, err) != ErrInvalidDropPoint {
		t.Fatalf("drop outside should be %v", ErrInvalidDropPoint)
	}
	task, _ := s.Task(taskID)
	if task.Status != TaskInProgress {
		t.Fatalf("task status = %v, want %v", task.Status, TaskInProgress)
	}
}

func TestClockRollbackAndRejectedOpsKeepState(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 1), 100)
	mustRegister(t, s, rect("N1", NoParking, 10, 10, 20, 20, 1), 101)

	// Clock rollback: any earlier timestamp is rejected.
	if err := s.AddVehicle("v1", 99); errCode(t, err) != ErrClockRollback {
		t.Fatalf("want %v", ErrClockRollback)
	}
	// Equal timestamps are accepted.
	mustAddVehicle(t, s, "v1", 101)
	mustUnlock(t, s, "v1", 102)

	// A rejected return changes nothing: vehicle keeps riding, clock stays.
	if _, err := s.Return("v1", "u1", Point{15, 15}, 103); errCode(t, err) != ErrNoParkingReturn {
		t.Fatalf("want %v", ErrNoParkingReturn)
	}
	if _, err := s.Return("v1", "u1", Point{50, 50}, 103); err != nil {
		t.Fatalf("same timestamp after rejection must be accepted: %v", err)
	}
	if c, _ := s.Count("O1"); c != 1 {
		t.Fatalf("count = %d, want 1", c)
	}
}

func TestErrorPrecedence(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 1), 100)
	mustRegister(t, s, rect("N1", NoParking, 10, 10, 20, 20, 1), 101)
	// Fill O1.
	ride(t, s, "v1", 102)
	if _, err := s.Return("v1", "u1", Point{50, 50}, 103); err != nil {
		t.Fatalf("fill O1: %v", err)
	}
	// Pending task exists (recall from an outside return).
	ride(t, s, "v2", 104)
	res, err := s.Return("v2", "u1", Point{500, 500}, 105)
	if err != nil {
		t.Fatalf("outside return: %v", err)
	}
	taskID := res.TaskIDs[0]

	// Clock rollback (2) beats vehicle-not-found (5).
	_, err = s.Return("ghost", "u1", Point{50, 50}, 1)
	if got := errCode(t, err); got != ErrClockRollback {
		t.Fatalf("got %v, want %v", got, ErrClockRollback)
	}
	// Vehicle-not-riding (6) beats no-parking-return (7).
	_, err = s.Return("v1", "u1", Point{15, 15}, 106)
	if got := errCode(t, err); got != ErrVehicleNotRiding {
		t.Fatalf("got %v, want %v", got, ErrVehicleNotRiding)
	}
	// Fence-full (8) beats task-not-found (9).
	_, err = s.CompleteTask("T999", Point{50, 50}, 107)
	if got := errCode(t, err); got != ErrFenceFull {
		t.Fatalf("got %v, want %v", got, ErrFenceFull)
	}
	// Task-not-found (9) beats invalid-drop-point (12).
	_, err = s.CompleteTask("T999", Point{15, 15}, 108)
	if got := errCode(t, err); got != ErrTaskNotFound {
		t.Fatalf("got %v, want %v", got, ErrTaskNotFound)
	}
	// Task-not-in-progress (11) beats invalid-drop-point (12).
	_, err = s.CompleteTask(taskID, Point{15, 15}, 109)
	if got := errCode(t, err); got != ErrTaskNotInProgress {
		t.Fatalf("got %v, want %v", got, ErrTaskNotInProgress)
	}
}

func TestOutsideReturnFlow(t *testing.T) {
	s := newService(t)
	mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 10), 1)

	ride(t, s, "v1", 2)
	res, err := s.Return("v1", "u1", Point{500, 500}, 3)
	if err != nil {
		t.Fatalf("outside return: %v", err)
	}
	if !res.Outside || res.Fee != 5 || res.Reward != 0 || len(res.TaskIDs) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	task, _ := s.Task(res.TaskIDs[0])
	if task.Kind != RecallTask || task.DestFenceID != "O1" || task.VehicleID != "v1" {
		t.Fatalf("unexpected recall task: %+v", task)
	}
	// The vehicle is in transit: it can neither be unlocked nor returned.
	if err := s.Unlock("v1", 4); errCode(t, err) != ErrInvalidArgument {
		t.Fatalf("unlock in-transit: want %v", ErrInvalidArgument)
	}
	if _, err := s.Return("v1", "u1", Point{50, 50}, 5); errCode(t, err) != ErrVehicleNotRiding {
		t.Fatalf("return in-transit: want %v", ErrVehicleNotRiding)
	}
	// Ledger holds exactly the outside fee.
	ledger := s.Ledger()
	if len(ledger) != 1 || ledger[0].Kind != LedgerOutsideFee || ledger[0].Amount != 5 {
		t.Fatalf("ledger: %+v", ledger)
	}
	// Completing the recall task parks the vehicle in the drop fence.
	if err := s.ClaimTask(task.ID, "d1", 6); err != nil {
		t.Fatalf("claim: %v", err)
	}
	cres, err := s.CompleteTask(task.ID, Point{50, 50}, 7)
	if err != nil || cres.Moved != 1 || cres.FenceID != "O1" {
		t.Fatalf("complete: res=%+v err=%v", cres, err)
	}
	if c, _ := s.Count("O1"); c != 1 {
		t.Fatalf("O1 count = %d, want 1", c)
	}
	// The vehicle can be unlocked again, releasing the slot.
	mustUnlock(t, s, "v1", 8)
	if c, _ := s.Count("O1"); c != 0 {
		t.Fatalf("O1 count after unlock = %d, want 0", c)
	}
}

// registerGrid registers side*side disjoint operating fences on a grid and
// returns the service.
func registerGrid(t *testing.T, side int) *Service {
	t.Helper()
	s := newService(t)
	ts := int64(1)
	for i := 0; i < side; i++ {
		for j := 0; j < side; j++ {
			x0 := int64(i * 40)
			y0 := int64(j * 40)
			id := fmt.Sprintf("O_%d_%d", i, j)
			mustRegister(t, s, rect(id, Operating, x0, y0, x0+20, y0+20, 10), ts)
			ts++
		}
	}
	return s
}

func avgChecked(s *Service, rng *rand.Rand, span int64, queries int) float64 {
	total := 0
	for i := 0; i < queries; i++ {
		p := Point{X: rng.Int63n(span), Y: rng.Int63n(span)}
		_, checked := s.LocateStats(p)
		total += checked
	}
	return float64(total) / float64(queries)
}

// TestPointQueryScalesSublinearly proves point queries do not scan every
// fence: quadrupling the fence count must not quadruple the number of
// candidate fences examined per query.
func TestPointQueryScalesSublinearly(t *testing.T) {
	const queries = 2000
	small := registerGrid(t, 10) // 100 fences
	large := registerGrid(t, 20) // 400 fences

	avgSmall := avgChecked(small, rand.New(rand.NewSource(7)), 10*40, queries)
	avgLarge := avgChecked(large, rand.New(rand.NewSource(7)), 20*40, queries)
	t.Logf("avg candidates per query: 100 fences -> %.2f, 400 fences -> %.2f", avgSmall, avgLarge)
	if avgLarge > avgSmall*2+1 {
		t.Fatalf("candidate count grows with fence count: %.2f -> %.2f", avgSmall, avgLarge)
	}
}

// TestReplayDeterminism applies the same operation sequence to two fresh
// services and requires identical snapshots.
func TestReplayDeterminism(t *testing.T) {
	run := func() Dump {
		s := newService(t)
		mustRegister(t, s, rect("O1", Operating, 0, 0, 100, 100, 3), 1)
		mustRegister(t, s, rect("R1", Reward, 0, 0, 10, 10, 2), 2)
		mustRegister(t, s, rect("N1", NoParking, 20, 20, 30, 30, 1), 3)
		ts := int64(10)
		points := []Point{{5, 5}, {50, 50}, {25, 25}, {500, 500}, {50, 50}, {5, 5}}
		for i, p := range points {
			id := fmt.Sprintf("v%d", i)
			mustAddVehicle(t, s, id, ts)
			ts++
			mustUnlock(t, s, id, ts)
			ts++
			// Rejected returns (no-parking, full) are part of the
			// sequence and must replay identically too.
			_, _ = s.Return(id, "u1", p, ts)
			ts++
		}
		for _, task := range s.Tasks() {
			_ = s.ClaimTask(task.ID, "d1", ts)
			ts++
			_, _ = s.CompleteTask(task.ID, Point{5, 5}, ts)
			ts++
		}
		return s.Dump()
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay diverged:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

func errCode(t *testing.T, err error) ErrCode {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	return e.Code
}
