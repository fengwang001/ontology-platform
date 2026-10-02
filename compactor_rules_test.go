package ontology

import (
	"errors"
	"testing"
)

func baseCfg() Config {
	return Config{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2, P: 0}
}

// addRuns adds runs whose sizes are given oldest-first. ID k belongs to the
// k-th added run; Runs() reports them newest-first (reverse argument order).
func addRuns(t *testing.T, c *Compactor, sizes ...int64) {
	t.Helper()
	for i, s := range sizes {
		if _, err := c.AddRun(int64(i+1), s); err != nil {
			t.Fatalf("AddRun(size=%d): %v", s, err)
		}
	}
}

func runIDList(rr []Run) []int64 {
	ids := make([]int64, len(rr))
	for i, r := range rr {
		ids[i] = r.ID
	}
	return ids
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertErrIs(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []Config{
		{MinRuns: 1, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 7, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 0, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 1_000_001, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: -1, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 10_001, MinMerge: 2, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 1, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 5, MaxMerge: 4, Cmax: 2},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 0},
		{MinRuns: 3, MaxRuns: 6, A: 200, Rho: 1, MinMerge: 2, MaxMerge: 4, Cmax: 2, P: -1},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrParam) {
			t.Fatalf("case %d: got %v, want ErrParam", i, err)
		}
	}
}

func TestExampleFromSpec(t *testing.T) {
	// Spec newest-first list [3,3,6,50] is added oldest-first.
	c, _ := New(baseCfg())
	addRuns(t, c, 50, 6, 3, 3)
	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reason != ReasonSizeRatio || !equalInt64(p.Runs, []int64{4, 3, 2}) || p.Total != 12 {
		t.Fatalf("plan = %+v", p)
	}
	r, err := c.Done(11, p.ID, 11)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != 5 || r.Size != 11 || r.Created != 11 || r.Busy || r.Fails != 0 {
		t.Fatalf("done run = %+v", r)
	}
	rr := c.Runs()
	if !equalInt64(runIDList(rr), []int64{5, 1}) || rr[0].Size != 11 || rr[1].Size != 50 {
		t.Fatalf("runs = %+v", rr)
	}
	_, err = c.Pick(12)
	assertErrIs(t, err, ErrNotNeeded)
}

func TestSpaceAmpExample2(t *testing.T) {
	// Newest-first [10,10,10,12]: E=30, 3000 >= 200*12.
	c, _ := New(baseCfg())
	addRuns(t, c, 12, 10, 10, 10)
	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reason != ReasonSpaceAmp || len(p.Runs) != 4 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestSpaceAmpEqualityAndOffByOne(t *testing.T) {
	// Newest-first [20,80,50]: E=100 == A*S (equality) fires.
	c, _ := New(baseCfg())
	addRuns(t, c, 50, 80, 20)
	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reason != ReasonSpaceAmp || len(p.Runs) != 3 {
		t.Fatalf("equality plan = %+v", p)
	}

	// E=99: 9900 < 10000 by 1; Rho=0, MinMerge=3 blocks SizeRatio too.
	c2, _ := New(baseCfg())
	c2.cfg.Rho = 0
	c2.cfg.MinMerge = 3
	addRuns(t, c2, 50, 79, 20)
	_, err = c2.Pick(10)
	assertErrIs(t, err, ErrNotNeeded)
}

func TestSpaceAmpYieldsWhenBusy(t *testing.T) {
	// Newest-first [7,7,50,1000]: SizeRatio takes the two 7-sized new runs;
	// SpaceAmp stays off (E=64, 6400 < 200000).
	c, _ := New(baseCfg())
	addRuns(t, c, 1000, 50, 7, 7)
	p1, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Reason != ReasonSizeRatio || !equalInt64(p1.Runs, []int64{4, 3}) {
		t.Fatalf("p1 = %+v", p1)
	}
	// IDs 3,4 stay busy. A huge new run makes SpaceAmp eligible if nothing
	// were busy (E includes 2000); the busy pair forces it to yield, and
	// no SizeRatio segment crosses the busy block either.
	if _, err := c.AddRun(11, 2000); err != nil {
		t.Fatal(err)
	}
	_, err = c.Pick(12)
	assertErrIs(t, err, ErrNotNeeded)
}

func TestSizeRatioUsesAccumulator(t *testing.T) {
	cfg := baseCfg()
	cfg.MinMerge = 3
	c, _ := New(cfg)
	// Newest-first [3,3,5,50]: 5 merges on acc=6 (500<=606), though it
	// would fail against the previous run 3 (500>303).
	addRuns(t, c, 50, 5, 3, 3)
	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reason != ReasonSizeRatio || !equalInt64(p.Runs, []int64{4, 3, 2}) || p.Total != 11 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestSizeRatioEqualityExtendAndOffByOne(t *testing.T) {
	cfg := baseCfg()
	cfg.Rho = 0
	cfg.MinMerge = 3
	c, _ := New(cfg)
	addRuns(t, c, 50, 4, 4, 4)
	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p.Runs, []int64{4, 3, 2}) {
		t.Fatalf("plan = %+v", p)
	}

	c2, _ := New(cfg)
	addRuns(t, c2, 50, 9, 4, 4) // acc=8, next 9: 900 > 800 -> count 2
	_, err = c2.Pick(10)
	assertErrIs(t, err, ErrNotNeeded)
}

func TestSizeRatioBusyStartAndBusyMiddle(t *testing.T) {
	cfg := baseCfg()
	cfg.Rho = 40
	c, _ := New(cfg)
	// Newest-first [7,7,50,70,10000]: start ID5(7) merges ID4(7), then 50
	// fails. The busy pair sits directly at the newest end.
	addRuns(t, c, 10000, 70, 50, 7, 7)
	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p.Runs, []int64{5, 4}) {
		t.Fatalf("plan = %+v", p)
	}
	// IDs 4,5 stay busy. Newest ID7 (small, size 6) cannot extend into the
	// busy block; behind it IDs 2,3 (sizes 70,50) satisfy equality.
	// Add ID7 size 1000: its extension immediately hits busy ID5.
	if _, err := c.AddRun(11, 1000); err != nil {
		t.Fatal(err)
	}
	p2, err := c.Pick(12)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p2.Runs, []int64{3, 2}) || p2.Total != 120 {
		t.Fatalf("plan2 = %+v", p2)
	}
}

func TestSizeRatioMergeBounds(t *testing.T) {
	cfg := baseCfg()
	cfg.Rho = 10000
	c, _ := New(cfg)
	addRuns(t, c, 1000, 5, 4, 3, 2, 1) // MaxMerge truncates at 4
	p, err := c.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Runs) != 4 || p.Total != 10 {
		t.Fatalf("plan = %+v", p)
	}

	cfg2 := baseCfg()
	cfg2.Rho = 10000
	cfg2.MinMerge = 3
	c2, _ := New(cfg2)
	addRuns(t, c2, 50, 2, 1) // exactly MinMerge
	p2, err := c2.Pick(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Runs) != 3 || p2.Total != 53 {
		t.Fatalf("plan = %+v", p2)
	}
}

func TestCountReduceFormulaAndBusyWindow(t *testing.T) {
	c, _ := New(baseCfg())
	addRuns(t, c, 1000, 32, 16, 8, 4, 2, 1)
	p, err := c.Pick(100)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reason != ReasonCountReduce || !equalInt64(p.Runs, []int64{7, 6}) || p.Total != 3 {
		t.Fatalf("plan = %+v", p)
	}

	c2, _ := New(baseCfg())
	addRuns(t, c2, 100000, 1024, 512, 256, 128, 64, 32, 16, 8, 4, 2, 1)
	p2, err := c2.Pick(100)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Reason != ReasonCountReduce || !equalInt64(p2.Runs, []int64{12, 11, 10, 9}) {
		t.Fatalf("plan = %+v", p2)
	}

	c3, _ := New(baseCfg())
	addRuns(t, c3, 1000, 32, 16, 8, 4, 2, 1)
	for _, r := range c3.runs {
		if r.ID == 6 {
			r.Busy = true
		}
	}
	p3, err := c3.Pick(100)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(p3.Runs, []int64{5, 4}) {
		t.Fatalf("plan = %+v", p3)
	}
}
