package cron

import "testing"

func TestSpecExampleForbid(t *testing.T) {
	j, err := NewJob("0 * * * *", 0, 30, Forbid)
	if err != nil {
		t.Fatal(err)
	}
	r, err := j.Sync(185)
	if err != nil || !r.Fired || r.TaskID != 1 || r.FireTime != 180 {
		t.Fatalf("sync 185: %+v %v", r, err)
	}
	if j.LastScheduled() != 180 {
		t.Fatal("L should be 180")
	}
	r, err = j.Sync(245)
	if err != nil || r.Fired || !r.Skipped || r.FireTime != 240 {
		t.Fatalf("sync 245: %+v %v", r, err)
	}
	if j.Skipped() != 1 || j.LastScheduled() != 240 {
		t.Fatalf("skipped=%d L=%d", j.Skipped(), j.LastScheduled())
	}
}

func TestSpecExample100And101(t *testing.T) {
	j, err := NewJob("0 * * * *", 240, -1, Allow)
	if err != nil {
		t.Fatal(err)
	}
	// Firings 300..6300 inclusive = 101.
	if _, err := j.Sync(6300); err != ErrTooManyMissed {
		t.Fatalf("got %v", err)
	}
	if j.LastScheduled() != 240 || j.nfCalls > 102 {
		t.Fatalf("rejected sync changed state: L=%d calls=%d", j.LastScheduled(), j.nfCalls)
	}
	// Going backwards is legal: last successful sync watermark stayed at 240.
	r, err := j.Sync(6240) // 300..6240 = 100 firings
	if err != nil {
		t.Fatal(err)
	}
	if !r.Fired || r.FireTime != 6240 || r.TaskID != 1 {
		t.Fatalf("got %+v", r)
	}

	// Exactly 100 window firings also via deadline D on a fresh job.
	j2, _ := NewJob("* * * * *", 0, 99, Forbid)
	r2, err := j2.Sync(100) // t in [1,100]: exactly 100
	if err != nil {
		t.Fatalf("100 firings should succeed: %v", err)
	}
	if !r2.Fired || r2.FireTime != 100 {
		t.Fatalf("got %+v", r2)
	}
}

func TestWindowBoundaries(t *testing.T) {
	// spec fires every hour. created L=0, D=60.
	j, _ := NewJob("0 * * * *", 0, 60, Allow)

	// t == L is not counted: Sync(0) has empty window.
	r, err := j.Sync(0)
	if err != nil || r.Fired {
		t.Fatalf("sync at L: %+v %v", r, err)
	}
	if j.LastScheduled() != 0 {
		t.Fatal("L must not move on empty window")
	}

	// t == now-D is included: Sync(120) keeps t >= 60, so 60 and 120;
	// latest = 120 (t == now included).
	r, err = j.Sync(120)
	if err != nil || r.FireTime != 120 {
		t.Fatalf("boundaries: %+v %v", r, err)
	}
	if j.LastScheduled() != 120 {
		t.Fatal("L should be 120")
	}

	// t just inside window: Sync(179) with D=60 keeps t >= 119 -> only 120.
	j2, _ := NewJob("0 * * * *", 0, 60, Allow)
	r, _ = j2.Sync(179)
	if r.FireTime != 120 {
		t.Fatalf("180 should be outside the window, got %d", r.FireTime)
	}
}

func TestOnlyLatestReplayed(t *testing.T) {
	j, _ := NewJob("0 * * * *", 0, -1, Allow)
	r, err := j.Sync(300)
	if err != nil || !r.Fired || r.FireTime != 300 || r.TaskID != 1 {
		t.Fatalf("got %+v %v", r, err)
	}
	// Only one new task despite five missed firings.
	if ids := j.Active(); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("active = %v", ids)
	}
}

func TestPolicies(t *testing.T) {
	// Forbid: active task blocks catch-up, but L advances.
	j, _ := NewJob("0 * * * *", 0, -1, Forbid)
	if _, err := j.Sync(60); err != nil {
		t.Fatal(err)
	}
	r, err := j.Sync(120)
	if err != nil || r.Fired || !r.Skipped {
		t.Fatalf("forbid should skip: %+v %v", r, err)
	}
	if j.Skipped() != 1 || j.LastScheduled() != 120 {
		t.Fatal("forbid state wrong")
	}
	// After finishing, next sync starts a new task with a new id.
	if err := j.Finish(1, 130); err != nil {
		t.Fatal(err)
	}
	r, _ = j.Sync(180)
	if !r.Fired || r.TaskID != 2 {
		t.Fatalf("expected new task id 2, got %+v", r)
	}

	// Replace: active tasks terminated and counted, then a new task.
	k, _ := NewJob("0 * * * *", 0, -1, Replace)
	_, _ = k.Sync(60)
	// Overlap another allowed firing while task 1 active (manual sequence).
	r, err = k.Sync(120)
	if err != nil || !r.Fired || r.TaskID != 2 {
		t.Fatalf("replace sync: %+v %v", r, err)
	}
	if k.Replaced() != 1 {
		t.Fatalf("replaced count = %d", k.Replaced())
	}
	if ids := k.Active(); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("active after replace = %v", ids)
	}
	// A Replace with two active tasks (task 2 + a concurrent-created 3
	// is impossible through normal flow; simulate via direct map).
	k.active[3] = struct{}{}
	k.nextID = 3
	_, _ = k.Sync(180)
	if k.Replaced() != 3 {
		t.Fatalf("replaced count should be 3, got %d", k.Replaced())
	}

	// Allow: tasks accumulate.
	a, _ := NewJob("0 * * * *", 0, -1, Allow)
	_, _ = a.Sync(60)
	_, _ = a.Sync(120)
	if ids := a.Active(); len(ids) != 2 {
		t.Fatalf("allow active = %v", ids)
	}
}

func TestSuspend(t *testing.T) {
	j, _ := NewJob("0 * * * *", 0, -1, Allow)
	j.SetSuspend(true)
	if r, err := j.Sync(1000); err != nil || r.Fired {
		t.Fatalf("suspended sync: %+v %v", r, err)
	}
	if j.LastScheduled() != 0 {
		t.Fatal("suspend must not advance L")
	}
	if ids := j.Active(); len(ids) != 0 {
		t.Fatal("suspend must not create tasks")
	}
	// Resume: no automatic catch-up; next Sync sees the full window.
	j.SetSuspend(false)
	r, err := j.Sync(1000)
	if err != nil || !r.Fired || r.FireTime != 960 {
		t.Fatalf("resumed sync: %+v %v", r, err)
	}

	// Suspend still advances the watermark: a later earlier Sync is rejected.
	k, _ := NewJob("0 * * * *", 0, -1, Allow)
	k.SetSuspend(true)
	_, _ = k.Sync(100)
	if _, err := k.Sync(50); err != ErrClockBackwards {
		t.Fatalf("suspended watermark should advance, got %v", err)
	}
}

func TestRejectedSyncChangesNothing(t *testing.T) {
	j, _ := NewJob("0 * * * *", 0, -1, Forbid)
	_, _ = j.Sync(60)

	// Invalid time.
	if _, err := j.Sync(-1); err != ErrInvalidTime {
		t.Fatalf("got %v", err)
	}
	if _, err := j.Sync(maxMinute + 1); err != ErrInvalidTime {
		t.Fatalf("got %v", err)
	}
	// Clock backwards.
	if _, err := j.Sync(30); err != ErrClockBackwards {
		t.Fatalf("got %v", err)
	}
	// Too many missed.
	if _, err := j.Sync(20000); err != ErrTooManyMissed {
		t.Fatalf("got %v", err)
	}
	if j.LastScheduled() != 60 || j.Skipped() != 0 || j.Replaced() != 0 {
		t.Fatalf("state changed after rejects: L=%d", j.LastScheduled())
	}
	if ids := j.Active(); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("active changed: %v", ids)
	}
	// Watermark unchanged: Sync(60) still legal and reports skip.
	r, err := j.Sync(120)
	if err != nil || !r.Skipped {
		t.Fatalf("watermark changed by rejected sync: %+v %v", r, err)
	}
}

func TestErrorOrdering(t *testing.T) {
	j, _ := NewJob("0 * * * *", 100, -1, Forbid)
	// Invalid time beats clock backwards.
	if _, err := j.Sync(-1); err != ErrInvalidTime {
		t.Fatalf("got %v", err)
	}
	_, _ = j.Sync(200)
	// Clock backwards beats too many missed.
	if _, err := j.Sync(50); err != ErrClockBackwards {
		t.Fatalf("got %v", err)
	}
}

func TestNewJobValidation(t *testing.T) {
	if _, err := NewJob("bogus", 0, -1, Allow); err != ErrInvalidArgument {
		t.Fatalf("bad spec: %v", err)
	}
	if _, err := NewJob("* * * * *", -1, -1, Allow); err != ErrInvalidArgument {
		t.Fatal("created < 0")
	}
	if _, err := NewJob("* * * * *", maxMinute+1, -1, Allow); err != ErrInvalidArgument {
		t.Fatal("created too large")
	}
	if _, err := NewJob("* * * * *", 0, -2, Allow); err != ErrInvalidArgument {
		t.Fatal("D < -1")
	}
	if _, err := NewJob("* * * * *", 0, 1_000_000_001, Allow); err != ErrInvalidArgument {
		t.Fatal("D too large")
	}
	if _, err := NewJob("* * * * *", 0, -1, Policy(99)); err != ErrInvalidArgument {
		t.Fatal("bad policy")
	}
	if _, err := NewJob("* * * * *", 0, 1_000_000_000, Allow); err != nil {
		t.Fatalf("D = 1e9 should be legal: %v", err)
	}
}

func TestFinish(t *testing.T) {
	j, _ := NewJob("0 * * * *", 0, -1, Allow)
	if err := j.Finish(1, 0); err != ErrTaskNotFound {
		t.Fatalf("unknown task: %v", err)
	}
	_, _ = j.Sync(60)
	if err := j.Finish(1, 60); err != nil {
		t.Fatal(err)
	}
	if err := j.Finish(1, 60); err != ErrTaskNotFound {
		t.Fatalf("double finish: %v", err)
	}
	if err := j.Finish(2, -1); err != ErrInvalidTime {
		t.Fatalf("finish invalid time: %v", err)
	}
}

func TestNextFireCallBound(t *testing.T) {
	j, _ := NewJob("* * * * *", 0, -1, Allow)
	_, _ = j.Sync(60) // 60 firings
	if j.nfCalls != 61 {
		t.Fatalf("expected 61 calls (60 firings + sentinel), got %d", j.nfCalls)
	}
	_, err := j.Sync(7000)
	if err != ErrTooManyMissed {
		t.Fatal(err)
	}
	if j.nfCalls > 102 {
		t.Fatalf("NextFire calls %d exceed 102", j.nfCalls)
	}
}
