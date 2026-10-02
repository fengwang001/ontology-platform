package debloat

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Controller {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	return c
}

// --- Construction validation ---------------------------------------------

func TestIllegalConfigs(t *testing.T) {
	base := exampleCfg()
	cases := map[string]func(*Config){
		"g zero":          func(c *Config) { c.G = 0 },
		"g over bmin":     func(c *Config) { c.G = 2048 },
		"bmin below g":    func(c *Config) { c.Bmin = 512 },
		"b0 below bmin":   func(c *Config) { c.B0 = 0 },
		"bmax below b0":   func(c *Config) { c.Bmax = 2048 },
		"bmin not mult":   func(c *Config) { c.Bmin = 2000 },
		"bmax not mult":   func(c *Config) { c.Bmax = 33000 },
		"b0 not mult":     func(c *Config) { c.B0 = 5000 },
		"bmax over 2^30":  func(c *Config) { c.Bmax = 1<<30 + 1024 },
		"t zero":          func(c *Config) { c.T = 0 },
		"t too big":       func(c *Config) { c.T = 1_000_001 },
		"w zero":          func(c *Config) { c.W = 0 },
		"w too big":       func(c *Config) { c.W = 101 },
		"thu too big":     func(c *Config) { c.ThU = 1001 },
		"thd too big":     func(c *Config) { c.ThD = 101 },
		"kc zero":         func(c *Config) { c.Kc = 0 },
		"kc too big":      func(c *Config) { c.Kc = 101 },
		"c0 zero":         func(c *Config) { c.C0 = 0 },
		"c0 too big":      func(c *Config) { c.C0 = 10_001 },
		"pool zero":       func(c *Config) { c.Pool = 0 },
		"pool too big":    func(c *Config) { c.Pool = 1<<40 + 1 },
		"h negative":      func(c *Config) { c.H = -1 },
		"h too big":       func(c *Config) { c.H = 1001 },
		"pool cannot fit": func(c *Config) { c.Pool = 4096 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if _, err := New(cfg); !errors.Is(err, ErrIllegalConfig) {
				t.Fatalf("want ErrIllegalConfig, got %v (cfg=%+v)", err, cfg)
			}
		})
	}
}

func TestBoundaryConfigsAccepted(t *testing.T) {
	mustNew(t, Config{
		Bmin: 1 << 30, Bmax: 1 << 30, G: 1 << 30, B0: 1 << 30,
		T: 1_000_000, W: 100, ThU: 1000, ThD: 100, Kc: 100,
		C0: 1, Pool: 1 << 40, H: 1000,
	})
	mustNew(t, Config{
		Bmin: 1, Bmax: 1, G: 1, B0: 1,
		T: 1, W: 1, ThU: 0, ThD: 0, Kc: 1,
		C0: 1, Pool: 1, H: 0,
	})
	// Beff(C0) == B0 is acceptable.
	mustNew(t, Config{
		Bmin: 1024, Bmax: 32768, G: 1024, B0: 4096,
		T: 1, W: 1, ThU: 0, ThD: 0, Kc: 1,
		C0: 2, Pool: 8192, H: 0,
	})
}

// --- R / Dt / per / cand rounding ----------------------------------------

func TestRoundingWithLargeNumbers(t *testing.T) {
	// R*T reaches ~10^21 here; must not overflow int64.
	cfg := Config{
		Bmin: 1 << 30, Bmax: 1 << 30, G: 1 << 30, B0: 1 << 30,
		T: 1_000_000, W: 100, ThU: 1000, ThD: 100, Kc: 100,
		C0: 1, Pool: 1 << 40, H: 0,
	}
	c := mustNew(t, cfg)
	for i := 0; i < 100; i++ {
		r, err := c.Sample(1<<30, 1)
		if err != nil {
			t.Fatal(err)
		}
		wantR := uint64(1<<30) * 1000
		if r.R != wantR {
			t.Fatalf("sample %d R=%d want %d", i, r.R, wantR)
		}
		if r.Cand != 1<<30 || r.Cur != 1<<30 {
			t.Fatalf("sample %d result %+v", i, r)
		}
	}
}

func TestCeilDtAndPer(t *testing.T) {
	// Window (10,2),(10,3): R=floor(20*1000/5)=4000; T=3 ->
	// Dt=ceil(12)=12; per with C=3 is 4, with C=5 is ceil(12/5)=3.
	cfg := Config{
		Bmin: 1, Bmax: 1_000_000, G: 1, B0: 100,
		T: 3, W: 2, ThU: 0, ThD: 100, Kc: 1,
		C0: 3, Pool: 1 << 30, H: 0,
	}
	c := mustNew(t, cfg)
	if _, err := c.Sample(10, 2); err != nil {
		t.Fatal(err)
	}
	r2, _ := c.Sample(10, 3)
	t.Logf("two-sample result=%+v", r2)
	if r2.Action == ActionSkipped {
		t.Fatal("second sample unexpectedly Skipped")
	}
	if r2.R != 4000 || r2.Cand != 4 {
		t.Fatalf("ceil rounding wrong: %+v", r2)
	}

	if err := c.SetChannels(5); err != nil {
		t.Fatal(err)
	}
	r3, _ := c.Sample(10, 2) // window (10,3),(10,2): same sums
	if r3.Action == ActionSkipped {
		r3, _ = c.Sample(10, 2)
	}
	t.Logf("C=5 result=%+v", r3)
	if r3.Cand != 3 {
		t.Fatalf("per ceil with C=5 wrong: %+v", r3)
	}
}

func TestCandGranularityFloor(t *testing.T) {
	cfg := Config{
		Bmin: 1024, Bmax: 32768, G: 1024, B0: 4096,
		T: 100, W: 1, ThU: 1000, ThD: 100, Kc: 100,
		C0: 1, Pool: 1 << 30, H: 0,
	}
	c := mustNew(t, cfg)
	// 30000 B/1000ms -> R=30000; Dt=ceil(3000)=3000; cand=floor(3000/1024)*1024.
	r, _ := c.Sample(30000, 1000)
	if r.Cand != 2048 {
		t.Fatalf("cand granularity floor wrong: %+v", r)
	}
}

// TestWindowBeforeAndAfterFill checks floor(R) both before and after the
// window fills, and that the oldest sample is evicted at W+1.
func TestWindowBeforeAndAfterFill(t *testing.T) {
	cfg := Config{
		Bmin: 1, Bmax: 1 << 30, G: 1, B0: 1,
		T: 1, W: 3, ThU: 1000, ThD: 100, Kc: 100,
		C0: 1, Pool: 1 << 40, H: 0,
	}
	c := mustNew(t, cfg)
	type pt struct {
		b, d uint64
		want uint64
	}
	// R=floor(Sb*1000/Sd): (10,3)->3333; +(20,3): floor(30000/6)=5000;
	// +(30,4): floor(60000/10)=6000; eviction +(5,1): window 20,30,5 ->
	// floor(55000/8)=6875.
	points := []pt{
		{10, 3, 3333},
		{20, 3, 5000},
		{30, 4, 6000},
		{5, 1, 6875},
	}
	for i, p := range points {
		r, err := c.Sample(p.b, p.d)
		if err != nil {
			t.Fatal(err)
		}
		if r.Action == ActionSkipped {
			t.Fatalf("point %d unexpectedly Skipped", i)
		}
		if r.R != p.want {
			t.Fatalf("point %d R=%d want %d", i, r.R, p.want)
		}
	}
	if s := c.State(); s.WindowLen != 3 {
		t.Fatalf("window len=%d", s.WindowLen)
	}
}

// --- Hysteresis threshold boundaries -------------------------------------

func TestGrowThresholdEquality(t *testing.T) {
	// cur=4096, thU=25: boundary delta = 1024; cand=5120 is exactly +25%.
	cfg := Config{
		Bmin: 1, Bmax: 1 << 20, G: 1, B0: 4096,
		T: 1000, W: 1, ThU: 25, ThD: 100, Kc: 1,
		C0: 1, Pool: 1 << 30, H: 0,
	}
	c := mustNew(t, cfg)
	if r, _ := c.Sample(5120, 1000); r.Action != ActionApplied || r.Cur != 5120 {
		t.Fatalf("exact grow threshold must Apply: %+v", r)
	}
	c2 := mustNew(t, cfg)
	if r2, _ := c2.Sample(5119, 1000); r2.Action != ActionHold || r2.Cur != 4096 {
		t.Fatalf("one byte below grow threshold must Hold: %+v", r2)
	}
}

func TestShrinkThresholdEquality(t *testing.T) {
	// cur=4096, thD=50: cand=2048 exactly -50% -> Apply; 2049 -> Hold.
	cfg := Config{
		Bmin: 1, Bmax: 1 << 20, G: 1, B0: 4096,
		T: 1000, W: 1, ThU: 1000, ThD: 50, Kc: 1,
		C0: 1, Pool: 1 << 30, H: 0,
	}
	c := mustNew(t, cfg)
	if r, _ := c.Sample(2048, 1000); r.Action != ActionApplied || r.Cur != 2048 {
		t.Fatalf("exact shrink threshold must Apply: %+v", r)
	}
	c2 := mustNew(t, cfg)
	if r2, _ := c2.Sample(2049, 1000); r2.Action != ActionHold || r2.Cur != 4096 {
		t.Fatalf("one byte above shrink threshold must Hold: %+v", r2)
	}
}

func TestBypassViaBeffStillNeedsConfirmation(t *testing.T) {
	c := mustNew(t, exampleCfg())
	if r1, _ := c.Sample(100000, 1000); r1.Action != ActionPending || r1.Streak != 1 || r1.Cand != 32768 {
		t.Fatalf("Beff bypass still needs confirmation: %+v", r1)
	}
}

func TestBypassViaBminImmediate(t *testing.T) {
	// cand == Bmin bypasses thD=99 and applies immediately.
	cfg := Config{
		Bmin: 1024, Bmax: 32768, G: 1024, B0: 4096,
		T: 1, W: 1, ThU: 1000, ThD: 99, Kc: 10,
		C0: 1, Pool: 1 << 30, H: 0,
	}
	c := mustNew(t, cfg)
	if r, _ := c.Sample(0, 1000); r.Action != ActionApplied || r.Cur != 1024 || r.Cand != 1024 {
		t.Fatalf("Bmin bypass must Apply immediately: %+v", r)
	}
}

func TestConfirmationInterrupted(t *testing.T) {
	cfg := exampleCfg()
	cfg.Kc = 3
	c := mustNew(t, cfg)
	high := func() Result {
		r, err := c.Sample(100000, 1000)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := high(); r.Action != ActionPending || r.Streak != 1 {
		t.Fatalf("streak1: %+v", r)
	}
	if r := high(); r.Action != ActionPending || r.Streak != 2 {
		t.Fatalf("streak2: %+v", r)
	}
	// Interrupt with a Hold: C=16 -> Beff=4096 == cur.
	if err := c.SetChannels(16); err != nil {
		t.Fatal(err)
	}
	if r := high(); r.Action != ActionHold {
		t.Fatalf("interrupt Hold: %+v", r)
	}
	if s := c.State(); s.Streak != 0 {
		t.Fatalf("streak after Hold=%d", s.Streak)
	}
	// Restart, then interrupt by forced shrink (C=32 -> Beff=2048).
	if err := c.SetChannels(2); err != nil {
		t.Fatal(err)
	}
	if r := high(); r.Action != ActionPending || r.Streak != 1 {
		t.Fatalf("restart streak: %+v", r)
	}
	if err := c.SetChannels(32); err != nil {
		t.Fatal(err)
	}
	s := c.State()
	if s.Cur != 2048 || s.Streak != 0 || s.ForcedCount != 1 || !s.Polluted {
		t.Fatalf("forced shrink state: %+v", s)
	}
}

func TestSkippedDoesNotEnterWindowOrTouchStreak(t *testing.T) {
	c := mustNew(t, exampleCfg())
	c.Sample(100000, 1000)
	c.Sample(100000, 1000) // Applied -> pol
	if r, _ := c.Sample(7, 7); r.Action != ActionSkipped {
		t.Fatalf("want Skipped, got %+v", r)
	}
	s := c.State()
	if s.Polluted || s.WindowLen != 2 || s.Streak != 0 || s.SkippedCount != 1 {
		t.Fatalf("Skipped changed state: %+v", s)
	}
	r2, _ := c.Sample(20000, 1000)
	if r2.R != 73333 { // window still holds 100k,100k (then +20k)
		t.Fatalf("R after skip=%d (window leaked?)", r2.R)
	}
}

func TestSetChannelsSemantics(t *testing.T) {
	c := mustNew(t, exampleCfg())
	if err := c.SetChannels(2); err != nil { // no-op
		t.Fatal(err)
	}
	if err := c.SetChannels(0); !errors.Is(err, ErrIllegalArgument) {
		t.Fatalf("C=0: %v", err)
	}
	if err := c.SetChannels(10_001); !errors.Is(err, ErrIllegalArgument) {
		t.Fatalf("C=10001: %v", err)
	}
	if err := c.SetChannels(65); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("C=65 Beff=0: %v", err)
	}
	if s := c.State(); s.Channels != 2 {
		t.Fatalf("rejected calls changed channels: %+v", s)
	}
	// No-op preserves streak.
	if r, _ := c.Sample(100000, 1000); r.Streak != 1 {
		t.Fatalf("pending: %+v", r)
	}
	if err := c.SetChannels(2); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); s.Streak != 1 {
		t.Fatalf("no-op reset streak: %+v", s)
	}
	// C=8 fits (Beff=8192) and does not force-shrink cur=4096.
	if err := c.SetChannels(8); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); s.Cur != 4096 || s.ForcedCount != 0 {
		t.Fatalf("C=8 should not shrink: %+v", s)
	}
}

func TestPauseResumeSemantics(t *testing.T) {
	c := mustNew(t, exampleCfg())
	c.Sample(100000, 1000)
	c.Sample(100000, 1000) // Applied: pol=true, lastDir=up
	c.Pause()
	if _, err := c.Sample(0, 0); !errors.Is(err, ErrIllegalArgument) {
		t.Fatalf("arg has priority over paused: %v", err)
	}
	if _, err := c.Sample(1, 1); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused: %v", err)
	}
	s := c.State()
	if !s.Paused || !s.Polluted || s.WindowLen != 2 {
		t.Fatalf("paused state changed: %+v", s)
	}
	c.Resume()
	s = c.State()
	if s.Paused || s.WindowLen != 0 || s.Streak != 0 {
		t.Fatalf("resume must clear window/streak: %+v", s)
	}
	if !s.Polluted || s.LastDir != DirUp || s.Damp != 0 {
		t.Fatalf("resume must preserve pol/lastDir/damp: %+v", s)
	}
	if r, _ := c.Sample(1, 1); r.Action != ActionSkipped {
		t.Fatalf("pol survives resume; want Skipped got %+v", r)
	}
}

// --- Oscillation suppression ---------------------------------------------

func newDampScenario(t *testing.T, h int) *Controller {
	t.Helper()
	cfg := exampleCfg()
	cfg.H = h
	c := mustNew(t, cfg)
	c.Sample(100000, 1000)
	c.Sample(100000, 1000) // Applied up; gap=0
	c.Sample(1, 1)         // Skipped: no gap progress
	c.Sample(20000, 1000)  // Hold gap=1
	c.Sample(0, 1000)      // Hold gap=2
	c.Sample(0, 1000)      // Applied down at gap=3
	return c
}

func TestDampArmedWhenGapEqualsH(t *testing.T) {
	c := newDampScenario(t, 3)
	s := c.State()
	if s.Cur != 3072 || s.Damp != 3 || s.LastDir != DirDown {
		t.Fatalf("damp arm: %+v", s)
	}
}

func TestDampNotArmedAtGapHPlusOne(t *testing.T) {
	c := newDampScenario(t, 2)
	if s := c.State(); s.Cur != 3072 || s.Damp != 0 {
		t.Fatalf("gap=3 > H=2 must not arm: %+v", s)
	}
}

func TestDampDoublesNeedAndAges(t *testing.T) {
	c := newDampScenario(t, 3)
	if err := c.SetChannels(8); err != nil { // Beff=8192, no shrink
		t.Fatal(err)
	}
	if r, _ := c.Sample(1, 1); r.Action != ActionSkipped { // forced shrink set pol
		t.Fatalf("want Skipped: %+v", r)
	}
	if s := c.State(); s.Damp != 3 || s.Gap != 0 {
		t.Fatalf("Skipped must not age damp/gap: %+v", s)
	}
	grow := func(wantStreak int, wantDamp int, wantAction Action) {
		t.Helper()
		r, err := c.Sample(100000, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if r.Action != wantAction || r.Streak != wantStreak {
			t.Fatalf("grow: %+v", r)
		}
		if s := c.State(); s.Damp != wantDamp {
			t.Fatalf("damp=%d want %d (r=%+v)", s.Damp, wantDamp, r)
		}
	}
	grow(1, 2, ActionPending)
	grow(2, 1, ActionPending) // streak == Kc but need == 2Kc
	grow(3, 0, ActionPending) // Kc < streak < 2Kc
	// damp is 0 at the start of this sample: need reverts to Kc=2; streak=4.
	r, _ := c.Sample(100000, 1000)
	if r.Action != ActionApplied || r.Cur != 8192 {
		t.Fatalf("final grow: %+v", r)
	}
	s := c.State()
	if s.LastDir != DirUp || s.Damp != 0 || s.Gap != 0 {
		t.Fatalf("final state: %+v", s)
	}
}

func TestForcedShrinkOscillationArming(t *testing.T) {
	// Up change, then within H samples a forced shrink (down) arms damp;
	// same-direction forced shrink afterwards must not re-arm or reset decay.
	cfg := exampleCfg()
	cfg.H = 5
	c := mustNew(t, cfg)
	c.Sample(100000, 1000)
	c.Sample(100000, 1000)                    // up Applied
	c.Sample(1, 1)                            // Skipped (no gap)
	c.Sample(20000, 1000)                     // Hold gap=1
	if err := c.SetChannels(32); err != nil { // Beff=2048 < cur 32768
		t.Fatal(err)
	}
	s := c.State()
	if s.Cur != 2048 || s.Damp != 5 || s.LastDir != DirDown || s.ForcedCount != 1 {
		t.Fatalf("forced shrink must arm damp at gap=1: %+v", s)
	}
	// Skipped due to forced shrink pol; damp must not age.
	if r, _ := c.Sample(1, 1); r.Action != ActionSkipped {
		t.Fatalf("want Skipped after forced shrink: %+v", r)
	}
	if s := c.State(); s.Damp != 5 {
		t.Fatalf("Skipped aged damp: %+v", s)
	}
	// Further forced shrink in the same (down) direction: no new arming.
	if err := c.SetChannels(64); err != nil { // Beff=1024
		t.Fatal(err)
	}
	if s := c.State(); s.Damp != 5 || s.LastDir != DirDown {
		t.Fatalf("same-direction forced shrink must not touch damp: %+v", s)
	}
}

func TestSetChannelsAndResumePreserveDamp(t *testing.T) {
	c := newDampScenario(t, 3)
	if err := c.SetChannels(1); err != nil { // Beff grows, no shrink
		t.Fatal(err)
	}
	if s := c.State(); s.Damp != 3 {
		t.Fatalf("SetChannels changed damp: %+v", s)
	}
	c.Pause()
	c.Resume()
	if s := c.State(); s.Damp != 3 {
		t.Fatalf("Resume changed damp: %+v", s)
	}
}

// --- windowOps: identical increments regardless of W ---------------------

func TestWindowOpsIndependentOfW(t *testing.T) {
	run := func(w int) []int {
		cfg := exampleCfg()
		cfg.W = w
		c := mustNew(t, cfg)
		prev := 0
		var ops []int
		for i := 0; i < w+20; i++ {
			r, err := c.Sample(uint64(20000+i*137), 1000)
			if err != nil {
				t.Fatal(err)
			}
			if r.Action == ActionSkipped { // pol after Applied: retry once
				i--
				continue
			}
			s := c.State()
			ops = append(ops, s.WindowOps-prev)
			prev = s.WindowOps
		}
		return ops
	}
	for _, w := range []int{3, 100} {
		ops := run(w)
		for i := 0; i < w; i++ {
			if ops[i] != 1 {
				t.Fatalf("W=%d fill sample %d cost %d, want 1 (ops=%v)", w, i, ops[i], ops[:w+2])
			}
		}
		for i := w; i < len(ops); i++ {
			if ops[i] != 2 {
				t.Fatalf("W=%d full sample %d cost %d, want 2 (ops=%v)", w, i, ops[i], ops[:w+2])
			}
		}
	}
	// Same-kind comparison: any full window sample costs 2 independent of W.
	ops3 := run(3)
	ops100 := run(100)
	if ops3[3] != 2 || ops100[100] != 2 || ops3[3] != ops100[100] {
		t.Fatalf("full-window increments differ across W: %d vs %d", ops3[3], ops100[100])
	}
	if ops3[0] != 1 || ops100[0] != 1 {
		t.Fatalf("fill-window increments differ across W: %d vs %d", ops3[0], ops100[0])
	}
}

// --- Invariants over a mixed replay --------------------------------------

func checkInvariants(t *testing.T, c *Controller, where string) {
	t.Helper()
	s := c.State()
	if s.Cur < c.cfg.Bmin || s.Cur > effectiveCap(c.cfg, s.Channels) {
		t.Fatalf("[%s] cur %d outside [%d,%d]", where, s.Cur, c.cfg.Bmin, effectiveCap(c.cfg, s.Channels))
	}
	if s.Cur%c.cfg.G != 0 {
		t.Fatalf("[%s] cur %d not multiple of G", where, s.Cur)
	}
	if s.Damp < 0 || s.Damp > c.cfg.H {
		t.Fatalf("[%s] damp %d outside [0,%d]", where, s.Damp, c.cfg.H)
	}
	if s.Streak < 0 || s.WindowLen < 0 || s.WindowLen > c.cfg.W {
		t.Fatalf("[%s] bad streak/window: %+v", where, s)
	}
}

func TestRejectedCallsChangeNothing(t *testing.T) {
	c := mustNew(t, exampleCfg())
	c.Sample(100000, 1000) // Pending streak=1
	before := c.State()
	c.Pause()
	if _, err := c.Sample(1, 1); err == nil {
		t.Fatal("paused sample accepted")
	}
	if _, err := c.Sample(1<<30+1, 1); !errors.Is(err, ErrIllegalArgument) {
		t.Fatalf("arg while paused: %v", err)
	}
	c.Resume()
	if err := c.SetChannels(65); err == nil {
		t.Fatal("insufficient capacity accepted")
	}
	after := c.State()
	// Resume legitimately clears window/streak/pause; compare only fields that
	// rejected operations must leave alone.
	if after.Cur != before.Cur || after.Channels != before.Channels ||
		after.Polluted != before.Polluted || after.Gap != before.Gap ||
		after.Damp != before.Damp || after.LastDir != before.LastDir ||
		after.AppliedCount != before.AppliedCount || after.ForcedCount != before.ForcedCount ||
		after.SkippedCount != before.SkippedCount || after.WindowOps != before.WindowOps {
		t.Fatalf("rejected op changed durable state:\nbefore=%+v\nafter =%+v", before, after)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := mustNew(t, exampleCfg())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(seed)))
			for i := 0; i < 500; i++ {
				switch r.Intn(6) {
				case 0:
					c.Sample(uint64(r.Intn(100001)), uint64(1+r.Intn(1000)))
				case 1:
					c.SetChannels(1 + r.Intn(40))
				case 2:
					c.Pause()
				case 3:
					c.Resume()
				default:
					c.State()
				}
				checkInvariants(t, c, "concurrent")
			}
		}(g)
	}
	wg.Wait()
}

func TestEnumStrings(t *testing.T) {
	cases := map[fmt.Stringer]string{
		DirNone:        "none",
		DirUp:          "up",
		DirDown:        "down",
		ActionApplied:  "Applied",
		ActionHold:     "Hold",
		ActionPending:  "Pending",
		ActionSkipped:  "Skipped",
		ActionRejected: "Rejected",
		Action(99):     "Rejected",
		Direction(99):  "none",
	}
	for v, want := range cases {
		if got := v.String(); got != want {
			t.Fatalf("%v.String()=%q want %q", v, got, want)
		}
	}
}
