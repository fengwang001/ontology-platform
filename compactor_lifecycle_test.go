package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestPeriodicEqualityOldestAndDoneReset(t *testing.T) {
	cfg := baseCfg()
	cfg.A = 1_000_000 // SpaceAmp off
	cfg.Rho = 0
	cfg.MinMerge = 100
	cfg.MaxMerge = 100
	cfg.MaxRuns = 100
	cfg.P = 5
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// IDs 1..5 created at 1..5 (oldest first order), newest-first list is
	// [5,4,3,2,1]. At now=8, runs created<=3 qualify; position-oldest is ID1.
	addRuns(t, c, 1, 2, 3, 4, 5)
	p, err := c.Pick(8)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reason != ReasonPeriodic || !equalInt64(p.Runs, []int64{1}) || p.Total != 1 {
		t.Fatalf("plan = %+v", p)
	}
	// ID1 busy: next-oldest eligible position is ID2 (created=2).
	p2, err := c.Pick(8)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p2.Runs, []int64{2}) || p2.Total != 2 {
		t.Fatalf("plan2 = %+v", p2)
	}
	// Done resets created: replacement occupies ID1's old position.
	r, err := c.Done(8, p.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != 6 || r.Created != 8 || r.Fails != 0 || r.Busy {
		t.Fatalf("replacement = %+v", r)
	}
	rr := c.Runs()
	// Newest-first: 5,4,3,2,6 (oldest ID1 replaced in place).
	if !equalInt64(runIDList(rr), []int64{5, 4, 3, 2, 6}) {
		t.Fatalf("runs = %v", runIDList(rr))
	}
	// now=12: replacement age is 4 < P, stays ineligible; oldest eligible
	// is now ID2 (created=2).
	if err := c.Abort(p2.ID); err != nil {
		t.Fatal(err)
	}
	p3, err := c.Pick(12)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p3.Runs, []int64{2}) || p3.Total != 2 {
		t.Fatalf("plan3 = %+v", p3)
	}
}

func TestPeriodicExactBoundary(t *testing.T) {
	cfg := baseCfg()
	cfg.A = 1_000_000
	cfg.Rho = 0
	cfg.MinMerge = 100
	cfg.MaxMerge = 100
	cfg.MaxRuns = 100
	cfg.P = 5
	c, _ := New(cfg)
	if _, err := c.AddRun(1, 7); err != nil {
		t.Fatal(err)
	}
	// now=5: age 4, not yet.
	if _, err := c.Pick(5); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("got %v, want ErrNotNeeded", err)
	}
	// now=6: age 5 exactly, fires.
	p, err := c.Pick(6)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p.Runs, []int64{1}) {
		t.Fatalf("plan = %+v", p)
	}
}

func TestCmaxBusyBeforeNotNeeded(t *testing.T) {
	cfg := baseCfg()
	cfg.Cmax = 1
	c, _ := New(cfg)
	addRuns(t, c, 50, 6, 3, 3)
	p1, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	// Slot consumed. With Cmax=1 the next Pick must return ErrBusy even
	// though the remaining runs would otherwise also yield a plan or not.
	_, err = c.Pick(11)
	assertErrIs(t, err, ErrBusy)
	// After Done, slot frees; only two runs remain -> ErrNotNeeded.
	if _, err := c.Done(12, p1.ID, 11); err != nil {
		t.Fatal(err)
	}
	_, err = c.Pick(13)
	assertErrIs(t, err, ErrNotNeeded)
}

func TestAbortReselectAndFailsExcludesSizeRatio(t *testing.T) {
	cfg := baseCfg()
	cfg.A = 1_000_000 // SpaceAmp off initially
	cfg.Rho = 0       // only equal sizes merge
	c, _ := New(cfg)
	// Newest-first [7,7,50,60].
	addRuns(t, c, 60, 50, 7, 7)
	p1, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p1.Runs, []int64{4, 3}) {
		t.Fatalf("p1 = %+v", p1)
	}
	if err := c.Abort(p1.ID); err != nil {
		t.Fatal(err)
	}
	// fails=1: same pair selectable again.
	p2, err := c.Pick(11)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p2.Runs, []int64{4, 3}) {
		t.Fatalf("p2 = %+v", p2)
	}
	if err := c.Abort(p2.ID); err != nil {
		t.Fatal(err)
	}
	// fails=2: SizeRatio excludes IDs 3,4 everywhere; 50/60 unequal, so no
	// plan via SizeRatio.
	if _, err := c.Pick(12); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("got %v, want ErrNotNeeded", err)
	}
	// SpaceAmp ignores fails: big new run dominates the oldest 60.
	c.cfg.A = 200
	if _, err := c.AddRun(13, 1000); err != nil {
		t.Fatal(err)
	}
	p3, err := c.Pick(14)
	if err != nil {
		t.Fatal(err)
	}
	// Newest-first: ID5,4,3,2,1.
	if p3.Reason != ReasonSpaceAmp || !equalInt64(p3.Runs, []int64{5, 4, 3, 2, 1}) {
		t.Fatalf("p3 = %+v", p3)
	}
}

func TestDoneReplacementPositionAndIDs(t *testing.T) {
	cfg := baseCfg()
	cfg.Rho = 10
	c, _ := New(cfg)
	// addRuns args oldest-first: IDs 1..5 = 8,4,4,3,1, so newest-first is
	// [1(ID5),3(ID4),4(ID3),4(ID2),8(ID1)].
	//  start ID5(1): cannot extend over 3
	//  start ID4(3): cannot extend over 4
	//  start ID3(4): merges ID2(4), then ID1(8): 800 <= 8*110
	addRuns(t, c, 8, 4, 4, 3, 1)
	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p.Runs, []int64{3, 2, 1}) || p.Total != 16 {
		t.Fatalf("plan = %+v", p)
	}
	r, err := c.Done(11, p.ID, 6)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != 6 || r.Created != 11 || r.Fails != 0 || r.Busy {
		t.Fatalf("replacement = %+v", r)
	}
	rr := c.Runs()
	if !equalInt64(runIDList(rr), []int64{5, 4, 6}) {
		t.Fatalf("runs = %v", runIDList(rr))
	}
	if rr[2].Size != 6 { // replacement at the old (oldest) position
		t.Fatalf("replacement not at replaced segment position: %+v", rr)
	}
	_, err = c.Done(12, p.ID, 1)
	assertErrIs(t, err, ErrUnknown)
	assertErrIs(t, c.Abort(p.ID), ErrUnknown)
	id, err := c.AddRun(12, 7)
	if err != nil {
		t.Fatal(err)
	}
	if id != 7 {
		t.Fatalf("new run id = %d, want 7", id)
	}
}

func TestErrorOrderingAndNoStateChange(t *testing.T) {
	c, _ := New(baseCfg())
	addRuns(t, c, 50, 6, 3, 3)

	// Param beats clock.
	if _, err := c.AddRun(-5, 0); !errors.Is(err, ErrParam) {
		t.Fatalf("AddRun: got %v, want ErrParam", err)
	}

	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Done(5, p.ID, 11)
	assertErrIs(t, err, ErrClock)
	_, err = c.Pick(5)
	assertErrIs(t, err, ErrClock)

	_, err = c.AddRun(5, 0)
	assertErrIs(t, err, ErrParam)
	_, err = c.Done(5, p.ID, 0)
	assertErrIs(t, err, ErrParam)
	_, err = c.Done(11, 999, 11)
	assertErrIs(t, err, ErrUnknown)

	// Rejected operations consumed no run IDs.
	r, err := c.Done(11, p.ID, 11)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != 5 {
		t.Fatalf("id = %d, want 5", r.ID)
	}
	assertErrIs(t, c.Abort(12345), ErrUnknown)
}

func TestConcurrentCalls(t *testing.T) {
	c, _ := New(Config{MinRuns: 2, MaxRuns: 100, A: 100, Rho: 10000,
		MinMerge: 2, MaxMerge: 8, Cmax: 4, P: 0})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			now := int64(100 + i*10)
			if _, err := c.AddRun(now, int64(1+i%5)); err != nil {
				return
			}
			if p, err := c.Pick(now + 1); err == nil {
				if i%2 == 0 {
					_ = c.Abort(p.ID)
				} else {
					_, _ = c.Done(now+2, p.ID, 1)
				}
			}
			_ = c.Runs()
		}(i)
	}
	wg.Wait()
}
