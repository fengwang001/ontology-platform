package demand

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		ContractDemandKW:   100, // 100 kW * 60 s = 6000 kW*s per window
		WindowSeconds:      60,
		SlipSeconds:        20,
		MaxPhysicalPowerKW: 10000,
	}
}

func mustController(t *testing.T, cfg Config) *Controller {
	t.Helper()
	c, err := NewController(cfg)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func mustAddLoad(t *testing.T, c *Controller, spec LoadSpec) {
	t.Helper()
	if err := c.AddLoad(spec); err != nil {
		t.Fatalf("AddLoad(%d): %v", spec.ID, err)
	}
}

func mustReport(t *testing.T, c *Controller, ts int64, energy float64) ReportResult {
	t.Helper()
	res, err := c.Report(ts, energy)
	if err != nil {
		t.Fatalf("Report(%d, %v): %v", ts, energy, err)
	}
	return res
}

func wantActions(t *testing.T, res ReportResult, stillViolating bool, want ...Action) {
	t.Helper()
	if res.StillViolating != stillViolating {
		t.Fatalf("StillViolating = %v, want %v (actions %v)", res.StillViolating, stillViolating, res.Actions)
	}
	if len(res.Actions) != len(want) {
		t.Fatalf("actions = %v, want %v", res.Actions, want)
	}
	for i, a := range res.Actions {
		if a != want[i] {
			t.Fatalf("actions = %v, want %v", res.Actions, want)
		}
	}
}

func cut(id int) Action     { return Action{Kind: ActionCut, LoadID: id} }
func restore(id int) Action { return Action{Kind: ActionRestore, LoadID: id} }

// Prediction landing exactly on contractDemand*window is NOT a violation.
func TestExactContractDemandNotViolation(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 1})
	mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 60, Priority: 5})

	mustReport(t, c, 0, 0) // first report only sets the clock
	// Constant power 100 kW: every open window predicts exactly 6000 kW*s.
	res := mustReport(t, c, 60, 6000)
	wantActions(t, res, false)

	// A hair above 100 kW tips the prediction over the limit.
	res = mustReport(t, c, 80, 2001) // power 100.05 kW
	wantActions(t, res, false, cut(2))
}

// Windows end exactly at absolute multiples of the slip; a report landing
// on a boundary closes the window and records its measured demand.
func TestWindowEndAlignmentAndPeak(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 1})

	mustReport(t, c, 0, 0)
	for ts := int64(20); ts <= 60; ts += 20 {
		res := mustReport(t, c, ts, 2000) // constant 100 kW
		wantActions(t, res, false)
	}
	peak, ok := c.PeakDemand()
	if !ok {
		t.Fatal("expected a recorded peak")
	}
	if peak.DemandKW != 100 || peak.WindowEnd != 60 {
		t.Fatalf("peak = %+v, want {100, windowEnd 60}", peak)
	}
}

// Equal peak demands resolve to the earliest window end.
func TestPeakTiePicksEarliest(t *testing.T) {
	c := mustController(t, testConfig())
	mustReport(t, c, 0, 0)
	mustReport(t, c, 60, 6000)  // 100 kW
	mustReport(t, c, 120, 6000) // 100 kW
	peak, ok := c.PeakDemand()
	if !ok {
		t.Fatal("expected a recorded peak")
	}
	// Windows ending 60, 80, 100, 120 all measure exactly 100 kW.
	if peak.DemandKW != 100 || peak.WindowEnd != 60 {
		t.Fatalf("peak = %+v, want {100, windowEnd 60}", peak)
	}
}

// Min-on exactly met allows cutting; one second short forbids it.
func TestMinOnExactlyMetAndOneSecondShort(t *testing.T) {
	reports := func(c *Controller) ReportResult {
		mustReport(t, c, 0, 0)
		mustReport(t, c, 20, 1000)        // 50 kW
		return mustReport(t, c, 30, 1500) // 150 kW -> violation, bound 90, need 60
	}

	t.Run("exactly met", func(t *testing.T) {
		c := mustController(t, testConfig())
		mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 1})
		mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 60, Priority: 5, MinOnSeconds: 30})
		res := reports(c) // at t=30 the load has been connected exactly 30 s
		wantActions(t, res, false, cut(2))
	})

	t.Run("one second short", func(t *testing.T) {
		c := mustController(t, testConfig())
		mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 1})
		mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 60, Priority: 5, MinOnSeconds: 31})
		res := reports(c) // 30 s < 31 s: no cuttable load remains
		wantActions(t, res, true)
	})
}

// Min-off exactly met allows restoring; one second short forbids it.
func TestMinOffExactlyMetAndOneSecondShort(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 1})
	mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 60, Priority: 5, MinOffSeconds: 30})

	mustReport(t, c, 0, 0)
	mustReport(t, c, 20, 1000)
	res := mustReport(t, c, 30, 1500) // cut at t=30
	wantActions(t, res, false, cut(2))

	res = mustReport(t, c, 40, 100) // 10 kW, disconnected 10 s
	wantActions(t, res, false)
	res = mustReport(t, c, 59, 190) // 10 kW, disconnected 29 s < 30 s
	wantActions(t, res, false)
	res = mustReport(t, c, 60, 10) // 10 kW, disconnected exactly 30 s
	wantActions(t, res, false, restore(2))
}

// The three cut-selection tie-break layers: fewest loads, then largest
// priority-number sum, then lexicographically smallest ID sequence.
func TestCutTieBreak(t *testing.T) {
	loads := []LoadSpec{
		{ID: 1, RatedPowerKW: 1000, Priority: 0}, // critical (min priority)
		{ID: 2, RatedPowerKW: 1000, Priority: 1},
		{ID: 3, RatedPowerKW: 60, Priority: 8},
		{ID: 4, RatedPowerKW: 60, Priority: 9},
		{ID: 5, RatedPowerKW: 60, Priority: 9},
	}

	t.Run("fewest loads beats priority sum", func(t *testing.T) {
		c := mustController(t, testConfig())
		for _, s := range loads {
			mustAddLoad(t, c, s)
		}
		mustReport(t, c, 0, 0)
		// 200 kW -> bound 80, need 120: load 2 alone (count 1, priority
		// sum 1) beats {4,5} (count 2, priority sum 18).
		res := mustReport(t, c, 10, 2000)
		wantActions(t, res, false, cut(2))
	})

	t.Run("priority sum then lexicographic", func(t *testing.T) {
		c := mustController(t, testConfig())
		for _, s := range loads {
			mustAddLoad(t, c, s)
		}
		mustReport(t, c, 0, 0)
		// 140 kW -> bound 92, need 48: any single load suffices.
		// Priority sum 9 wins for {4} and {5}; lexicographic picks 4.
		// Load 3 (id 3 < 4 but priority 8 < 9) must NOT win.
		res := mustReport(t, c, 10, 1400)
		wantActions(t, res, false, cut(4))
	})
}

// Restore tries loads in ascending priority and stops at the first one
// whose restoration would violate; later loads are not skipped to.
func TestRestoreStopsAtFirstFailure(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 0}) // critical
	mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 90, Priority: 1})
	mustAddLoad(t, c, LoadSpec{ID: 3, RatedPowerKW: 10, Priority: 2})
	mustAddLoad(t, c, LoadSpec{ID: 4, RatedPowerKW: 10, Priority: 3})

	mustReport(t, c, 0, 0)
	// 200 kW -> bound 80, need 120; candidates sum to 110 < 120: cut all.
	res := mustReport(t, c, 10, 2000)
	wantActions(t, res, true, cut(2), cut(3), cut(4))

	// 80 kW -> bound 80. Restoring load 2 (90 kW) would violate, so the
	// whole pass stops: loads 3 and 4 must NOT be restored either.
	res = mustReport(t, c, 20, 800)
	wantActions(t, res, false)

	// 1 kW -> bound 100. Load 2 restores (91 <= 100); load 3 would reach
	// 101 > 100 and stops the pass, so load 4 stays disconnected.
	res = mustReport(t, c, 80, 60)
	wantActions(t, res, false, restore(2))
}

// Critical loads (minimum priority number) are never cut, even when that
// leaves the violation unresolved.
func TestCriticalLoadNeverCut(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 500, Priority: 0}) // critical
	mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 50, Priority: 1})

	mustReport(t, c, 0, 0)
	// 500 kW -> bound 20, need 480. Only load 2 (50 kW) is cuttable.
	res := mustReport(t, c, 10, 5000)
	wantActions(t, res, true, cut(2))
}

// Locked loads are excluded from cutting; unlocking re-enables
// participation from the next evaluation on.
func TestLockedLoadNotCut(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 500, Priority: 5})
	mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 50, Priority: 1}) // critical

	if err := c.LockLoad(1); err != nil {
		t.Fatalf("LockLoad: %v", err)
	}
	mustReport(t, c, 0, 0)
	// 500 kW -> violation, but load 1 is locked and load 2 is critical.
	res := mustReport(t, c, 10, 5000)
	wantActions(t, res, true)

	// Unlocking does not evaluate by itself; the next report cuts load 1.
	if err := c.UnlockLoad(1); err != nil {
		t.Fatalf("UnlockLoad: %v", err)
	}
	res = mustReport(t, c, 20, 5000)
	wantActions(t, res, true, cut(1)) // window already overrun: still violating
}

// Locking a disconnected load neither restores it nor triggers an
// evaluation; the load is restored only by a later normal evaluation.
func TestLockDisconnectedDoesNotRestore(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 0}) // critical
	mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 90, Priority: 1})

	mustReport(t, c, 0, 0)
	res := mustReport(t, c, 10, 2000) // 200 kW, need 120, only 90 available
	wantActions(t, res, true, cut(2))

	if err := c.LockLoad(2); err != nil {
		t.Fatalf("LockLoad: %v", err)
	}
	// The lock itself produced no action; the next evaluation (no
	// violation, min-off met) restores the load normally.
	res = mustReport(t, c, 20, 0)
	wantActions(t, res, false, restore(2))
}

// Error categories are distinguishable and checked in the required order:
// invalid parameter > illegal data > time regression.
func TestErrorPrecedence(t *testing.T) {
	c := mustController(t, testConfig())
	mustReport(t, c, 10, 100)

	cases := []struct {
		name   string
		ts     int64
		energy float64
		want   error
	}{
		{"negative energy beats regression", 5, -1, ErrInvalidParam},
		{"negative timestamp", -1, 0, ErrInvalidParam},
		{"regressed with positive energy is illegal data", 5, 10, ErrDataIllegal},
		{"over physical limit", 20, 1000000, ErrDataIllegal},
		{"plain regression", 10, 0, ErrTimeRegression},
		{"regressed zero energy", 5, 0, ErrTimeRegression},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Report(tc.ts, tc.energy)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Report(%d, %v) err = %v, want %v", tc.ts, tc.energy, err, tc.want)
			}
		})
	}
}

// Maintenance-op error order: invalid parameter > not found > state.
func TestOpErrorPrecedence(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 10, Priority: 1})

	if err := c.LockLoad(0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("LockLoad(0) = %v, want ErrInvalidParam", err)
	}
	if err := c.LockLoad(99); !errors.Is(err, ErrLoadNotFound) {
		t.Fatalf("LockLoad(99) = %v, want ErrLoadNotFound", err)
	}
	if err := c.LockLoad(1); err != nil {
		t.Fatalf("LockLoad(1) = %v", err)
	}
	if err := c.LockLoad(1); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("re-LockLoad(1) = %v, want ErrStateNotAllowed", err)
	}
	if err := c.UnlockLoad(1); err != nil {
		t.Fatalf("UnlockLoad(1) = %v", err)
	}
	if err := c.UnlockLoad(1); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("re-UnlockLoad(1) = %v, want ErrStateNotAllowed", err)
	}
	if err := c.AddLoad(LoadSpec{ID: 1, RatedPowerKW: 10, Priority: 2}); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("duplicate AddLoad = %v, want ErrStateNotAllowed", err)
	}
	if err := c.AddLoad(LoadSpec{ID: 2, RatedPowerKW: -5, Priority: 2}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad-spec AddLoad = %v, want ErrInvalidParam", err)
	}
}

// Invalid configurations are rejected.
func TestInvalidConfig(t *testing.T) {
	base := testConfig()
	bads := []Config{
		{ContractDemandKW: 0, WindowSeconds: 60, SlipSeconds: 20, MaxPhysicalPowerKW: 1},
		{ContractDemandKW: 1, WindowSeconds: 0, SlipSeconds: 20, MaxPhysicalPowerKW: 1},
		{ContractDemandKW: 1, WindowSeconds: 60, SlipSeconds: 0, MaxPhysicalPowerKW: 1},
		{ContractDemandKW: 1, WindowSeconds: 60, SlipSeconds: 25, MaxPhysicalPowerKW: 1}, // not a multiple
		{ContractDemandKW: 1, WindowSeconds: 60, SlipSeconds: 20, MaxPhysicalPowerKW: 0},
	}
	for i, cfg := range bads {
		if _, err := NewController(cfg); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("bad config %d: err = %v, want ErrInvalidParam", i, err)
		}
	}
	if _, err := NewController(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// Deleting a connected or disconnected load is rejected; deleting a
// non-existent load reports not-found.
func TestRemoveLoadRejected(t *testing.T) {
	c := mustController(t, testConfig())
	mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 0})
	mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 90, Priority: 1})

	if err := c.RemoveLoad(0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("RemoveLoad(0) = %v, want ErrInvalidParam", err)
	}
	if err := c.RemoveLoad(99); !errors.Is(err, ErrLoadNotFound) {
		t.Fatalf("RemoveLoad(99) = %v, want ErrLoadNotFound", err)
	}
	if err := c.RemoveLoad(2); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("RemoveLoad(connected 2) = %v, want ErrStateNotAllowed", err)
	}

	mustReport(t, c, 0, 0)
	res := mustReport(t, c, 10, 2000) // cuts load 2
	wantActions(t, res, true, cut(2))
	if err := c.RemoveLoad(2); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("RemoveLoad(disconnected 2) = %v, want ErrStateNotAllowed", err)
	}
}

// A rejected report changes nothing: time is not advanced, the ledger is
// untouched, and later results equal a controller that never saw it.
func TestRejectedReportLeavesNoTrace(t *testing.T) {
	cfg := testConfig()
	main := mustController(t, cfg)
	ctrl := mustController(t, cfg)
	for _, c := range []*Controller{main, ctrl} {
		mustAddLoad(t, c, LoadSpec{ID: 1, RatedPowerKW: 1000, Priority: 0})
		mustAddLoad(t, c, LoadSpec{ID: 2, RatedPowerKW: 60, Priority: 5})
	}

	mustReport(t, main, 10, 100)
	mustReport(t, ctrl, 10, 100)

	// Rejected attempts against main only.
	for _, r := range [][2]float64{{5, 10}, {20, -1}, {20, 1e9}, {10, 0}} {
		if _, err := main.Report(int64(r[0]), r[1]); err == nil {
			t.Fatalf("Report(%v, %v) unexpectedly accepted", r[0], r[1])
		}
	}

	// A timestamp between the rejected ones is still acceptable: time did
	// not advance. From here both controllers must agree exactly.
	for _, rep := range [][2]int64{{15, 50}, {25, 100}, {26, 10}} {
		gotMain := mustReport(t, main, rep[0], float64(rep[1]))
		gotCtrl := mustReport(t, ctrl, rep[0], float64(rep[1]))
		if fmt.Sprint(gotMain) != fmt.Sprint(gotCtrl) {
			t.Fatalf("after t=%d: main %v != control %v", rep[0], gotMain, gotCtrl)
		}
	}
	pMain, okMain := main.PeakDemand()
	pCtrl, okCtrl := ctrl.PeakDemand()
	if okMain != okCtrl || pMain != pCtrl {
		t.Fatalf("peaks differ: %v %v vs %v %v", pMain, okMain, pCtrl, okCtrl)
	}
}

// The first accepted report only sets the clock; its energy is not
// attributed to any interval.
func TestFirstReportBaseline(t *testing.T) {
	c := mustController(t, testConfig())
	res := mustReport(t, c, 100, 500) // energy ignored: no interval exists
	wantActions(t, res, false)
	mustReport(t, c, 110, 100) // 10 kW over (100,110]
	mustReport(t, c, 130, 0)
	peak, ok := c.PeakDemand()
	if !ok {
		t.Fatal("expected a recorded peak")
	}
	// Window ending 120 saw only the 100 kW*s of interval (100,110].
	if peak.DemandKW != 100.0/60.0 || peak.WindowEnd != 120 {
		t.Fatalf("peak = %+v, want {100/60, windowEnd 120}", peak)
	}
}

// The energy ledger retains at most window/slip boundary entries no
// matter how long the report history grows.
func TestBoundaryRetentionBounded(t *testing.T) {
	cfg := Config{ContractDemandKW: 100000, WindowSeconds: 60, SlipSeconds: 10, MaxPhysicalPowerKW: 10000}
	c := mustController(t, cfg)
	mustReport(t, c, 0, 0)
	for ts := int64(1); ts <= 5000; ts++ {
		mustReport(t, c, ts, 1)
	}
	if got, want := len(c.hist.cum), int(cfg.WindowSeconds/cfg.SlipSeconds); got > want {
		t.Fatalf("retained boundaries = %d, want <= %d", got, want)
	}
}

// Concurrent calls are serialized by the controller mutex; run with -race.
func TestConcurrencySmoke(t *testing.T) {
	c := mustController(t, testConfig())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := g*100 + 1
			_ = c.AddLoad(LoadSpec{ID: id, RatedPowerKW: 10, Priority: g})
			for i := 0; i < 200; i++ {
				_, _ = c.Report(int64(i), float64(i%7)) // mostly rejected: fine
				_ = c.LockLoad(id)
				_ = c.UnlockLoad(id)
				_, _ = c.PeakDemand()
			}
		}(g)
	}
	wg.Wait()
	if _, ok := c.PeakDemand(); !ok {
		t.Fatal("expected a recorded peak after concurrent reports")
	}
}
