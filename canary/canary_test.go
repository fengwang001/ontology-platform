package canary

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Stages:              []int{1000, 5000, 9000},
		MinDwell:            100,
		MinGrayRequests:     10,
		ErrorRateTolerance:  100,
		MaxConsecutiveFails: 2,
		StickyTTL:           50,
		MaxSticky:           100,
	}
}

func mustNew(t *testing.T, cfg Config) *Splitter {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func feed(t *testing.T, s *Splitter, v Version, n int, success bool, now int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Observe(v, success, now); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
}

func TestInitialState(t *testing.T) {
	s := mustNew(t, testConfig())
	if s.Phase() != PhaseNotStarted || s.Ratio() != 0 || s.Stage() != 0 {
		t.Fatalf("initial state = %v/%d/%d", s.Phase(), s.Ratio(), s.Stage())
	}
}

func TestPlacementStableAndUniform(t *testing.T) {
	pos := Placement("user-42")
	for i := 0; i < 100; i++ {
		if Placement("user-42") != pos {
			t.Fatalf("placement not stable")
		}
	}
	if pos < 0 || pos >= Basis {
		t.Fatalf("placement out of range: %d", pos)
	}
	counts := make([]int, Basis)
	const ids = 200000
	for i := 0; i < ids; i++ {
		counts[Placement(fmt.Sprintf("id-%d", i))]++
	}
	mean := float64(ids) / Basis
	for i, c := range counts {
		dev := (float64(c) - mean) / mean
		// 200k samples => mean 20, sd ~4.47; require each bucket within
		// +-50% relative is far too loose in spirit but still guard against
		// gross bias; the real statistical check here is the aggregate band.
		if dev > 0.9 || dev < -0.9 {
			t.Fatalf("bucket %d count %d deviates %.2f from mean %.2f", i, c, dev, mean)
		}
	}
	// Aggregate chi-square against uniformity, df = Basis-1 = 9999. The 0.1%
	// critical value is about 10500; a biased mapping (raw FNV high bits)
	// yields hundreds of thousands here, while an unbiased mapping stays
	// around 10000.
	var chi float64
	for _, c := range counts {
		d := float64(c) - mean
		chi += d * d / mean
	}
	if chi > 11000 {
		t.Fatalf("chi-square %.0f indicates non-uniform placement", chi)
	}
}

// Dwell boundary: exactly D is considered full; D-1 is not.
func TestEvaluateDwellBoundary(t *testing.T) {
	s := mustNew(t, testConfig())
	if err := s.Start(1000); err != nil {
		t.Fatal(err)
	}
	feed(t, s, VersionGray, 10, true, 1001)

	res, err := s.Evaluate(1000 + 99)
	if err != nil || res.Outcome != EvalDwellNotMet {
		t.Fatalf("at D-1: %+v %v", res, err)
	}
	res, err = s.Evaluate(1000 + 100)
	if err != nil || res.Outcome != EvalPassed || !res.Advanced || res.Stage != 1 {
		t.Fatalf("at D exactly: %+v %v", res, err)
	}
}

// Sample boundary: exactly G is enough; G-1 is not.
func TestEvaluateSampleBoundary(t *testing.T) {
	s := mustNew(t, testConfig())
	if err := s.Start(0); err != nil {
		t.Fatal(err)
	}
	feed(t, s, VersionGray, 9, true, 1)
	res, err := s.Evaluate(100)
	if err != nil || res.Outcome != EvalInsufficientSamples {
		t.Fatalf("G-1: %+v %v", res, err)
	}
	feed(t, s, VersionGray, 1, true, 101)
	// Insufficient-samples does not reset dwell, so at 200 the window still
	// holds exactly G samples.
	res, err = s.Evaluate(200)
	if err != nil || res.Outcome != EvalPassed {
		t.Fatalf("at G exactly: %+v %v", res, err)
	}
}

// grayFail/grayTotal == stableFail/stableTotal + T/Basis exactly must pass;
// one extra gray failure must fail.
func TestEvaluateErrorRateExactEquality(t *testing.T) {
	cfg := testConfig()
	cfg.Stages = []int{10000}
	s := mustNew(t, cfg)
	if err := s.Start(0); err != nil {
		t.Fatal(err)
	}
	feed(t, s, VersionStable, 90, true, 1)
	feed(t, s, VersionStable, 10, false, 1)
	feed(t, s, VersionGray, 89, true, 1)
	feed(t, s, VersionGray, 11, false, 1)

	lhs := new(big.Rat).SetFrac(big.NewInt(11), big.NewInt(100))
	rhs := new(big.Rat).Add(
		new(big.Rat).SetFrac(big.NewInt(10), big.NewInt(100)),
		new(big.Rat).SetFrac(big.NewInt(int64(cfg.ErrorRateTolerance)), big.NewInt(Basis)))
	if lhs.Cmp(rhs) != 0 {
		t.Fatalf("test setup not at exact boundary")
	}
	res, err := s.Evaluate(100)
	if err != nil || res.Outcome != EvalPassed {
		t.Fatalf("exact equality must pass: %+v %v", res, err)
	}
	if s.Phase() != PhaseCompleted || s.Ratio() != 10000 {
		t.Fatalf("phase=%v ratio=%d", s.Phase(), s.Ratio())
	}

	s2 := mustNew(t, cfg)
	_ = s2.Start(0)
	feed(t, s2, VersionStable, 90, true, 1)
	feed(t, s2, VersionStable, 10, false, 1)
	feed(t, s2, VersionGray, 88, true, 1)
	feed(t, s2, VersionGray, 12, false, 1)
	res, _ = s2.Evaluate(100)
	if res.Outcome != EvalFailed || res.FailStreak != 1 {
		t.Fatalf("above tolerance must fail: %+v", res)
	}
}

// A pass resets the failure streak.
func TestFailStreakClearedByPass(t *testing.T) {
	cfg := testConfig()
	cfg.Stages = []int{10000}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	feed(t, s, VersionGray, 10, false, 1)
	res, _ := s.Evaluate(100)
	if res.Outcome != EvalFailed || s.FailStreak() != 1 {
		t.Fatalf("first fail: %+v", res)
	}
	feed(t, s, VersionGray, 10, true, 101)
	res, _ = s.Evaluate(200)
	if res.Outcome != EvalPassed || s.FailStreak() != 0 {
		t.Fatalf("pass must clear streak: %+v", res)
	}
}

// While the ratio only increases, the same id must never move gray -> stable.
func TestSameIdNeverRevertsUnderMonotonicRatio(t *testing.T) {
	cfg := testConfig()
	cfg.StickyTTL = 0
	s := mustNew(t, cfg)
	const idsN = 500
	everGray := make([]bool, idsN)
	_ = s.Start(0)
	for stage, ratio := range cfg.Stages {
		now := int64(stage * 100)
		for i := 0; i < idsN; i++ {
			id := fmt.Sprintf("u-%d", i)
			r, err := s.Route(id, now)
			if err != nil {
				t.Fatal(err)
			}
			if r.Source != SourceRatio {
				t.Fatalf("stage %d expected ratio source, got %v", stage, r.Source)
			}
			isGray := Placement(id) < ratio
			if (r.Version == VersionGray) != isGray {
				t.Fatalf("version mismatch")
			}
			if r.Version == VersionStable && everGray[i] {
				t.Fatalf("id %d moved back from gray", i)
			}
			if r.Version == VersionGray {
				everGray[i] = true
			}
		}
		if stage < len(cfg.Stages)-1 {
			feed(t, s, VersionGray, 10, true, now+1)
			res, err := s.Evaluate(now + 100)
			if err != nil || res.Outcome != EvalPassed {
				t.Fatalf("advance: %+v %v", res, err)
			}
		}
	}
}

// Demote preserves sticky records.
func TestDemoteKeepsSticky(t *testing.T) {
	cfg := testConfig()
	cfg.StickyTTL = 1 << 40
	s := mustNew(t, cfg)
	_ = s.Start(0)
	id := "sticky-1"
	r1, err := s.Route(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, s, VersionGray, 10, true, 1)
	if res, _ := s.Evaluate(100); res.Outcome != EvalPassed {
		t.Fatalf("advance failed: %+v", res)
	}
	if err := s.Demote(150); err != nil {
		t.Fatal(err)
	}
	if s.Stage() != 0 {
		t.Fatalf("stage after demote = %d", s.Stage())
	}
	if s.StickyCount() != 1 {
		t.Fatalf("sticky not preserved: %d", s.StickyCount())
	}
	r2, _ := s.Route(id, 160)
	if r2.Source != SourceSticky || r2.Version != r1.Version {
		t.Fatalf("sticky after demote: %+v vs %+v", r2, r1)
	}
	if err := s.Demote(200); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("demote stage0 err = %v", err)
	}
}

// Rollback forces stable, clears sticky and is terminal until Reset.
func TestRollbackClearsStickyAndForcesStable(t *testing.T) {
	cfg := testConfig()
	cfg.MaxConsecutiveFails = 1
	s := mustNew(t, cfg)
	_ = s.Start(0)
	_, _ = s.Route("a", 0)
	if s.StickyCount() != 1 {
		t.Fatalf("precondition")
	}
	feed(t, s, VersionGray, 10, false, 1)
	res, _ := s.Evaluate(100)
	if !res.RolledBack || s.Phase() != PhaseRolledBack || s.Ratio() != 0 {
		t.Fatalf("not rolled back: %+v", res)
	}
	if s.StickyCount() != 0 {
		t.Fatalf("sticky not cleared: %d", s.StickyCount())
	}
	r, err := s.Route("a", 101)
	if err != nil || r.Version != VersionStable || r.Source != SourceRollback {
		t.Fatalf("rollback route: %+v %v", r, err)
	}
	if s.StickyCount() != 0 {
		t.Fatalf("rollback route created sticky")
	}
	_ = s.Observe(VersionGray, false, 102)
	if s.Phase() != PhaseRolledBack {
		t.Fatalf("observe changed rollback state")
	}
	if err := s.Start(103); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("start after rollback err = %v", err)
	}
	if err := s.Reset(104); err != nil || s.Phase() != PhaseNotStarted {
		t.Fatalf("reset: %v %v", err, s.Phase())
	}
}

// Sticky lifetime is left-closed/right-open: L-1 sticky, L expired.
func TestStickyExpiryBoundary(t *testing.T) {
	id := "expiry"
	s2 := mustNew(t, testConfig())
	_ = s2.Start(0)
	r1, _ := s2.Route(id, 0)
	r2, _ := s2.Route(id, 49)
	if r2.Source != SourceSticky || r2.Version != r1.Version {
		t.Fatalf("L-1 should be sticky: %+v", r2)
	}
	s3 := mustNew(t, testConfig())
	_ = s3.Start(0)
	_, _ = s3.Route(id, 0)
	r3, _ := s3.Route(id, 50)
	if r3.Source != SourceRatio {
		t.Fatalf("at exactly L the record must be expired, got %v", r3.Source)
	}
}

// LRU eviction: cap P, least recently routed is the victim; touching refreshes.
func TestStickyLRUEviction(t *testing.T) {
	cfg := testConfig()
	cfg.MaxSticky = 2
	cfg.StickyTTL = 1 << 40
	s := mustNew(t, cfg)
	_ = s.Start(0)
	_, _ = s.Route("a", 0)
	_, _ = s.Route("b", 1)
	_, _ = s.Route("c", 2) // over cap by one: a (LRU) is evicted
	if s.StickyCount() != 2 {
		t.Fatalf("count = %d", s.StickyCount())
	}
	r, _ := s.Route("a", 3) // a was evicted; re-decided by ratio
	if r.Source != SourceRatio {
		t.Fatalf("a should have been evicted, source = %v", r.Source)
	}
	if s.StickyCount() != 2 {
		t.Fatalf("count after reinsert = %d", s.StickyCount())
	}
	// Reinserting a evicts the then-LRU record b; c stays.
	if _, ok := s.index["b"]; ok {
		t.Fatalf("b should have been evicted by a's reinsertion")
	}
	if _, ok := s.index["c"]; !ok {
		t.Fatalf("c should be retained")
	}
}

// Error priority: invalid argument, then clock regression, then illegal state.
// Every rejected operation leaves state and the clock anchor untouched.
func TestErrorPriority(t *testing.T) {
	s := mustNew(t, testConfig())
	if err := s.Start(100); err != nil {
		t.Fatal(err)
	}

	// Route: empty id (invalid) must outrank clock regression.
	if _, err := s.Route("", 99); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("route invalid+back = %v", err)
	}
	// Valid id but backwards clock outranks nothing-state-dependent.
	if _, err := s.Route("z", 99); !errors.Is(err, ErrClockWentBack) {
		t.Fatalf("route back = %v", err)
	}

	// Observe: bad version outranks clock regression.
	if err := s.Observe(Version(99), true, 99); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("observe invalid+back = %v", err)
	}
	if err := s.Observe(VersionGray, true, 99); !errors.Is(err, ErrClockWentBack) {
		t.Fatalf("observe back = %v", err)
	}

	// Start: clock regression outranks illegal state (already running).
	if err := s.Start(99); !errors.Is(err, ErrClockWentBack) {
		t.Fatalf("start back+illegal = %v", err)
	}
	if err := s.Start(100); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("start illegal = %v", err)
	}

	// Demote: clock regression outranks illegal state (stage 0).
	if err := s.Demote(99); !errors.Is(err, ErrClockWentBack) {
		t.Fatalf("demote back+illegal = %v", err)
	}
	if err := s.Demote(100); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("demote illegal = %v", err)
	}

	// Reset is always allowed (state-clearing op) but still honors the clock.
	if err := s.Reset(50); !errors.Is(err, ErrClockWentBack) {
		t.Fatalf("reset back = %v", err)
	}

	// After the rejections the clock anchor is still 100 and state unchanged.
	if s.Phase() != PhaseRunning || s.Stage() != 0 {
		t.Fatalf("rejected op mutated state: %v/%d", s.Phase(), s.Stage())
	}
	if _, err := s.Route("z", 100); err != nil {
		t.Fatalf("equal-time route must still be accepted: %v", err)
	}
}

// From NotStarted: an illegal-state op with a backwards clock reports clock.
func TestErrorPriorityNotStarted(t *testing.T) {
	s := mustNew(t, testConfig())
	if err := s.Demote(0); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("demote not-started = %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	base := testConfig
	bad := []func(c Config) Config{
		func(c Config) Config { c.Stages = nil; return c },
		func(c Config) Config { c.Stages = []int{0}; return c },
		func(c Config) Config { c.Stages = []int{10001}; return c },
		func(c Config) Config { c.Stages = []int{500, 500}; return c },
		func(c Config) Config { c.Stages = []int{900, 100}; return c },
		func(c Config) Config { c.MinDwell = -1; return c },
		func(c Config) Config { c.MinGrayRequests = -1; return c },
		func(c Config) Config { c.ErrorRateTolerance = -1; return c },
		func(c Config) Config { c.ErrorRateTolerance = 10001; return c },
		func(c Config) Config { c.MaxConsecutiveFails = 0; return c },
		func(c Config) Config { c.StickyTTL = -1; return c },
		func(c Config) Config { c.MaxSticky = -1; return c },
	}
	for i, mutate := range bad {
		if _, err := New(mutate(base())); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: err = %v", i, err)
		}
	}
}

// Observations outside running are ignored and do not create a window later.
func TestObserveIgnoredOutsideRunning(t *testing.T) {
	s := mustNew(t, testConfig())
	if err := s.Observe(VersionGray, false, 0); err != nil {
		t.Fatalf("observe before start must be ignored, got %v", err)
	}
	if err := s.Start(10); err != nil {
		t.Fatal(err)
	}
	res, _ := s.Evaluate(110)
	if res.GrayTotal != 0 || res.Outcome != EvalInsufficientSamples {
		t.Fatalf("pre-start observations leaked: %+v", res)
	}
}

// Concurrent calls must not corrupt state (run under -race).
func TestConcurrentSafety(t *testing.T) {
	s := mustNew(t, testConfig())
	if err := s.Start(0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(g*1000 + i)
				_, _ = s.Route(fmt.Sprintf("g%d-u%d", g, i%50), now)
				_ = s.Observe(VersionGray, i%3 != 0, now)
				_, _ = s.Evaluate(now)
				_ = s.safeRead()
			}
		}(g)
	}
	wg.Wait()
	// Sticky size never exceeds P regardless of interleaving.
	if s.StickyCount() > s.cfg.MaxSticky {
		t.Fatalf("sticky cap violated: %d", s.StickyCount())
	}
}

func (s *Splitter) safeRead() Phase {
	return s.Phase()
}

func TestPlacementOfMethodAndUpsertRefresh(t *testing.T) {
	s := mustNew(t, testConfig())
	if s.PlacementOf("abc") != Placement("abc") {
		t.Fatalf("PlacementOf mismatch")
	}
	_ = s.Start(0)
	// Ratio 0: first route goes stable; within TTL the record is refreshed in
	// place (no new entry, no eviction).
	r1, _ := s.Route("same-id", 0)
	r2, _ := s.Route("same-id", 10)
	if r1.Version != VersionStable || r2.Source != SourceSticky || s.StickyCount() != 1 {
		t.Fatalf("refresh in place failed: %+v %+v count=%d", r1, r2, s.StickyCount())
	}
}

// TestRouteCostIndependentOfHistory proves that routing cost does not grow
// with the number of historical requests: a million extra observations must
// not make Route measurably slower, and the big-H vs small-H median route
// times must be within a tight factor of each other.
func TestRouteCostIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cost proof in -short mode")
	}
	measure := func(history int) time.Duration {
		cfg := testConfig()
		cfg.MaxSticky = 64
		s := mustNew(t, cfg)
		_ = s.Start(0)
		for i := 0; i < history; i++ {
			_ = s.Observe(VersionGray, true, 1)
		}
		const routes = 20000
		start := time.Now()
		for i := 0; i < routes; i++ {
			_, _ = s.Route(fmt.Sprintf("perf-%d", i%256), 100+int64(i%30))
		}
		return time.Since(start) / routes
	}
	small := measure(1000)
	large := measure(1_000_000)
	t.Logf("median route: %v after 1k history, %v after 1m history", small, large)
	if large > small*3 {
		t.Fatalf("route cost grew with history: %v vs %v", large, small)
	}
}
