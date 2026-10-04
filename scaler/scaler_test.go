package scaler

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func exampleConfig() Config {
	return Config{
		Mn: 2, Mx: 20, C0: 4,
		H: 70, Lw: 30,
		OutTiers: []Tier{{0, 20}, {10, 50}, {20, 100}},
		InTiers:  []Tier{{0, 10}, {10, 30}},
		Ms:       1, W: 5, Cout: 10, Cin: 15,
	}
}

func mustNew(t *testing.T, cfg Config) *Scaler {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return s
}

func mustEval(t *testing.T, s *Scaler, now, metric int64) Result {
	t.Helper()
	r, err := s.Evaluate(now, metric)
	if err != nil {
		t.Fatalf("Evaluate(%d,%d): unexpected error: %v", now, metric, err)
	}
	return r
}

func checkResult(t *testing.T, got Result, kind Kind, amount, cap, inflight int64) {
	t.Helper()
	want := Result{Kind: kind, Amount: amount, Cap: cap, InFlight: inflight}
	if got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
}

// TestExampleSequence replays the worked example from the specification.
func TestExampleSequence(t *testing.T) {
	s := mustNew(t, exampleConfig())

	checkResult(t, mustEval(t, s, 0, 75), KindOut, 1, 4, 1)
	_, batches, b0, lastOut, _, hasOut, _, _ := s.Snapshot()
	if !reflect.DeepEqual(batches, []Batch{{5, 1}}) || b0 != 4 || lastOut != 0 || !hasOut {
		t.Fatalf("after (0,75): batches=%v b0=%d lastOut=%d hasOut=%v", batches, b0, lastOut, hasOut)
	}

	checkResult(t, mustEval(t, s, 3, 85), KindOut, 1, 4, 2)
	_, batches, b0, lastOut, _, _, _, _ = s.Snapshot()
	if !reflect.DeepEqual(batches, []Batch{{5, 1}, {8, 1}}) || b0 != 4 || lastOut != 0 {
		t.Fatalf("after (3,85): batches=%v b0=%d lastOut=%d", batches, b0, lastOut)
	}

	checkResult(t, mustEval(t, s, 5, 50), KindNone, 0, 5, 1)
	checkResult(t, mustEval(t, s, 6, 20), KindNone, 0, 5, 1)
	checkResult(t, mustEval(t, s, 8, 20), KindIn, 1, 5, 0)
	checkResult(t, mustEval(t, s, 10, 80), KindOut, 3, 5, 3)
	_, batches, b0, lastOut, _, _, _, _ = s.Snapshot()
	if !reflect.DeepEqual(batches, []Batch{{15, 3}}) || b0 != 5 || lastOut != 10 {
		t.Fatalf("after (10,80): batches=%v b0=%d lastOut=%d", batches, b0, lastOut)
	}
}

// TestThresholdEquality: metric==H triggers scale-out, metric==Lw does not
// trigger scale-in.
func TestThresholdEquality(t *testing.T) {
	cfg := exampleConfig()
	cfg.Cout = 100 // keep the second call inside cooldown so it stays a no-op
	s := mustNew(t, cfg)

	r := mustEval(t, s, 0, 70) // metric == H, v=0 -> first tier 20%
	checkResult(t, r, KindOut, 1, 4, 1)

	r = mustEval(t, s, 1, 30) // metric == Lw -> dead band, no scale-in
	checkResult(t, r, KindNone, 0, 4, 1)
}

// TestTierBoundary: v exactly equal to a tier lo belongs to that tier.
func TestTierBoundary(t *testing.T) {
	s := mustNew(t, exampleConfig())
	// v = 10 hits the (10,50) tier exactly: delta = max(1, ceil(4*50/100)) = 2.
	checkResult(t, mustEval(t, s, 0, 80), KindOut, 2, 4, 2)

	// Scale-in side: u = 10 hits the (10,30) tier exactly.
	cfg2 := exampleConfig()
	cfg2.C0 = 10
	s2 := mustNew(t, cfg2)
	// u = 30-20 = 10 -> 30% -> delta = max(1, floor(10*30/100)) = 3.
	checkResult(t, mustEval(t, s2, 0, 20), KindIn, 3, 7, 0)
}

// TestCeilVsMinStep covers ceil rounding and the max(ms, ...) trade-off.
func TestCeilVsMinStep(t *testing.T) {
	cfg := exampleConfig()
	cfg.Ms = 5
	s := mustNew(t, cfg)
	// ceil(4*20/100) = 1 < ms=5 -> delta = 5.
	checkResult(t, mustEval(t, s, 0, 75), KindOut, 5, 4, 5)

	s2 := mustNew(t, exampleConfig()) // ms = 1
	// ceil(4*20/100) = ceil(0.8) = 1 -> delta = 1 (not 0).
	checkResult(t, mustEval(t, s2, 0, 75), KindOut, 1, 4, 1)

	cfg3 := exampleConfig()
	cfg3.C0 = 7
	s3 := mustNew(t, cfg3)
	// ceil(7*20/100) = ceil(1.4) = 2 -> delta = 2.
	checkResult(t, mustEval(t, s3, 0, 75), KindOut, 2, 7, 2)
}

// TestCooldownUsesB0: inside cooldown the target is computed from B0, not
// from the current effective capacity.
func TestCooldownUsesB0(t *testing.T) {
	s := mustNew(t, exampleConfig())
	mustEval(t, s, 0, 75) // B0=4, batch (5,1), eff=5
	// At t=3 the batch is still in flight: eff=5. Cooldown base must be
	// B0=4: delta=ceil(4*50/100)=2, target=6, top-up = 6-5 = 1.
	checkResult(t, mustEval(t, s, 3, 85), KindOut, 1, 4, 2)
}

// TestCooldownNoWidening: inside cooldown, when target<=eff there is no
// action and lastOut is not updated.
func TestCooldownNoWidening(t *testing.T) {
	s := mustNew(t, exampleConfig())
	mustEval(t, s, 0, 75) // B0=4, eff becomes 5
	// Same tier (20%): target = 4+1 = 5 == eff -> no action.
	checkResult(t, mustEval(t, s, 3, 75), KindNone, 0, 4, 1)
	_, _, b0, lastOut, _, _, _, _ := s.Snapshot()
	if b0 != 4 || lastOut != 0 {
		t.Fatalf("lastOut/B0 changed inside cooldown: b0=%d lastOut=%d", b0, lastOut)
	}
}

// TestCooldownEndsAtEquality: now == lastOut+Cout is out of cooldown.
func TestCooldownEndsAtEquality(t *testing.T) {
	s := mustNew(t, exampleConfig())
	mustEval(t, s, 0, 75) // lastOut=0, batch (5,1)
	mustEval(t, s, 5, 50) // merge -> cap=5, eff=5
	// now=10 == lastOut+Cout: cooldown over, base=eff=5, 50% tier,
	// delta=ceil(2.5)=3, target=8 -> append 3.
	checkResult(t, mustEval(t, s, 10, 80), KindOut, 3, 5, 3)
	_, _, b0, lastOut, _, _, _, _ := s.Snapshot()
	if b0 != 5 || lastOut != 10 {
		t.Fatalf("b0=%d lastOut=%d, want 5/10", b0, lastOut)
	}

	// Contrast: now=9 is still inside cooldown and uses B0=4.
	s2 := mustNew(t, exampleConfig())
	mustEval(t, s2, 0, 75)
	mustEval(t, s2, 5, 50)
	checkResult(t, mustEval(t, s2, 9, 80), KindOut, 1, 5, 1)
	_, _, b0, lastOut, _, _, _, _ = s2.Snapshot()
	if b0 != 4 || lastOut != 0 {
		t.Fatalf("cooldown call updated lastOut/B0: b0=%d lastOut=%d", b0, lastOut)
	}
}

// TestClampToMaxNoLastOutUpdate: when the Mx clamp makes target==eff there
// is no action and lastOut is not updated.
func TestClampToMaxNoLastOutUpdate(t *testing.T) {
	cfg := Config{
		Mn: 1, Mx: 10, C0: 9,
		H: 100, Lw: 0,
		OutTiers: []Tier{{0, 100}},
		InTiers:  []Tier{{0, 10}},
		Ms:       1, W: 5, Cout: 10, Cin: 0,
	}
	s := mustNew(t, cfg)
	// delta=9, target=min(10,18)=10 > eff=9 -> append 1.
	checkResult(t, mustEval(t, s, 0, 100), KindOut, 1, 9, 1)
	mustEval(t, s, 5, 50) // merge -> cap=10
	// Out of cooldown; target=min(10,10+10)=10 == eff -> no action.
	checkResult(t, mustEval(t, s, 10, 100), KindNone, 0, 10, 0)
	_, _, _, lastOut, _, _, _, _ := s.Snapshot()
	if lastOut != 0 {
		t.Fatalf("lastOut updated on clamped no-op: %d", lastOut)
	}
}

// TestBatchReadyExactlyAtNow: a batch with ReadyAt==now merges.
func TestBatchReadyExactlyAtNow(t *testing.T) {
	s := mustNew(t, exampleConfig())
	mustEval(t, s, 0, 75) // batch (5,1)
	// Ready exactly at now=5: merges even though the metric is in the
	// dead band.
	checkResult(t, mustEval(t, s, 5, 50), KindNone, 0, 5, 0)
}

// TestInFlightBlocksScaleIn: any in-flight batch blocks scale-in; once
// ready, scale-in is allowed again.
func TestInFlightBlocksScaleIn(t *testing.T) {
	s := mustNew(t, exampleConfig())
	mustEval(t, s, 0, 75) // batch (5,1)
	checkResult(t, mustEval(t, s, 1, 20), KindNone, 0, 4, 1)
	// Batch ready at 5 -> cap=5, no in-flight: u=10 -> 30%,
	// delta=max(1,floor(1.5))=1 -> cap=4.
	checkResult(t, mustEval(t, s, 5, 20), KindIn, 1, 4, 0)
}

// TestScaleInFloorMinOneAndLowerClamp covers floor rounding, the minimum
// delta of 1 and the Mn clamp.
func TestScaleInFloorMinOneAndLowerClamp(t *testing.T) {
	cfg := Config{
		Mn: 1, Mx: 100, C0: 3,
		H: 100, Lw: 50,
		OutTiers: []Tier{{0, 10}},
		InTiers:  []Tier{{0, 10}},
		Ms:       1, W: 5, Cout: 0, Cin: 0,
	}
	s := mustNew(t, cfg)
	// floor(3*10/100)=0 -> min delta 1 -> cap=2.
	checkResult(t, mustEval(t, s, 0, 0), KindIn, 1, 2, 0)
	checkResult(t, mustEval(t, s, 1, 0), KindIn, 1, 1, 0)
	// cap==Mn: target=max(1,0)=1 == cap -> no action.
	checkResult(t, mustEval(t, s, 2, 0), KindNone, 0, 1, 0)

	// Lower clamp: pct=100 on cap=5 -> target=max(1,0)=1, amount=4.
	cfg2 := cfg
	cfg2.C0 = 5
	cfg2.InTiers = []Tier{{0, 100}}
	s2 := mustNew(t, cfg2)
	checkResult(t, mustEval(t, s2, 0, 0), KindIn, 4, 1, 0)
}

// TestScaleInCooldownIndependent: the two cooldowns run on independent
// clocks.
func TestScaleInCooldownIndependent(t *testing.T) {
	cfg := exampleConfig()
	cfg.Cout = 100
	cfg.Cin = 50
	s := mustNew(t, cfg)

	// Scale-in at t=0: delta=max(1,floor(4*30/100))=1 -> cap=3, lastIn=0.
	checkResult(t, mustEval(t, s, 0, 20), KindIn, 1, 3, 0)
	// Scale-out at t=1 is not affected by the scale-in cooldown.
	checkResult(t, mustEval(t, s, 1, 75), KindOut, 1, 3, 1)
	mustEval(t, s, 6, 50) // merge batch (6,1) -> cap=4
	// t=7 < lastIn+Cin=50: scale-in blocked by its own cooldown.
	checkResult(t, mustEval(t, s, 7, 20), KindNone, 0, 4, 0)
	// t=50 == lastIn+Cin: cooldown over -> cap=3, lastIn=50.
	checkResult(t, mustEval(t, s, 50, 20), KindIn, 1, 3, 0)
	// Scale-out cooldown still counts from lastOut=1, unaffected by the
	// scale-in at t=50: t=60 < 1+100 -> cooldown, base=B0=3.
	checkResult(t, mustEval(t, s, 60, 75), KindOut, 1, 3, 1)
	_, _, _, lastOut, _, _, _, _ := s.Snapshot()
	if lastOut != 1 {
		t.Fatalf("lastOut=%d, want 1 (cooldown top-up must not move it)", lastOut)
	}
}

// TestClockRegressionRejected: a regressed now is rejected and merges
// nothing.
func TestClockRegressionRejected(t *testing.T) {
	cfg := exampleConfig()
	cfg.W = 100
	s := mustNew(t, cfg)
	mustEval(t, s, 0, 75)  // batch (100,1)
	mustEval(t, s, 60, 50) // maxNow=60

	if _, err := s.Evaluate(50, 50); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("err=%v, want ErrClockRegression", err)
	}
	cap, batches, _, _, _, _, _, maxNow := s.Snapshot()
	if cap != 4 || len(batches) != 1 || maxNow != 60 {
		t.Fatalf("rejected call mutated state: cap=%d batches=%v maxNow=%d", cap, batches, maxNow)
	}
	// The batch merges only once an accepted now reaches 100.
	checkResult(t, mustEval(t, s, 100, 50), KindNone, 0, 5, 0)
}

// TestInvalidArgumentPrecedence: invalid arguments are reported before
// clock regression, and rejected calls change nothing.
func TestInvalidArgumentPrecedence(t *testing.T) {
	s := mustNew(t, exampleConfig())
	mustEval(t, s, 10, 50) // maxNow=10

	cases := []struct {
		now, metric int64
		want        error
	}{
		{-1, 50, ErrInvalidArgument},
		{1_000_000_000_000_001, 50, ErrInvalidArgument},
		{5, -1, ErrInvalidArgument},
		{5, 1_000_000_001, ErrInvalidArgument},
		{5, 50, ErrClockRegression}, // valid args, regressed clock
		{9, 75, ErrClockRegression},
	}
	for _, c := range cases {
		if _, err := s.Evaluate(c.now, c.metric); !errors.Is(err, c.want) {
			t.Fatalf("Evaluate(%d,%d): err=%v, want %v", c.now, c.metric, err, c.want)
		}
	}
	cap, batches, _, _, _, _, _, maxNow := s.Snapshot()
	if cap != 4 || len(batches) != 0 || maxNow != 10 {
		t.Fatalf("rejected calls mutated state: cap=%d batches=%v maxNow=%d", cap, batches, maxNow)
	}
}

// TestInvalidConfig: each violated constraint rejects the whole config.
func TestInvalidConfig(t *testing.T) {
	valid := exampleConfig()
	bad := []Config{}
	mut := func(f func(*Config)) {
		c := valid
		f(&c)
		bad = append(bad, c)
	}
	mut(func(c *Config) { c.Mn = 0 })
	mut(func(c *Config) { c.Mn = 1_000_001 })
	mut(func(c *Config) { c.Mx = c.Mn - 1 })
	mut(func(c *Config) { c.Mx = 1_000_001 })
	mut(func(c *Config) { c.C0 = c.Mn - 1 })
	mut(func(c *Config) { c.C0 = c.Mx + 1 })
	mut(func(c *Config) { c.H = 1_000_000_001 })
	mut(func(c *Config) { c.Lw = -1 })
	mut(func(c *Config) { c.Lw = c.H })
	mut(func(c *Config) { c.OutTiers = nil })
	mut(func(c *Config) { c.InTiers = nil })
	mut(func(c *Config) { c.OutTiers = []Tier{{1, 20}} })
	mut(func(c *Config) { c.OutTiers = []Tier{{0, 20}, {0, 30}} })
	mut(func(c *Config) { c.OutTiers = []Tier{{0, 20}, {5, 30}, {5, 40}} })
	mut(func(c *Config) { c.InTiers = []Tier{{0, 0}} })
	mut(func(c *Config) { c.InTiers = []Tier{{0, 1001}} })
	mut(func(c *Config) { c.Ms = 0 })
	mut(func(c *Config) { c.W = 0 })
	mut(func(c *Config) { c.W = 1_000_000_001 })
	mut(func(c *Config) { c.Cout = -1 })
	mut(func(c *Config) { c.Cin = 1_000_000_001 })
	for i, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("bad config %d: err=%v, want ErrInvalidConfig", i, err)
		}
	}
	if _, err := New(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// TestRepeatSameNowMetric: repeating the same metric at the same now is a
// no-op from the second evaluation on, even with zero cooldowns.
func TestRepeatSameNowMetric(t *testing.T) {
	cfg := exampleConfig()
	cfg.Cout = 0
	cfg.Cin = 0
	s := mustNew(t, cfg)

	checkResult(t, mustEval(t, s, 0, 75), KindOut, 1, 4, 1)
	checkResult(t, mustEval(t, s, 0, 75), KindNone, 0, 4, 1)
	checkResult(t, mustEval(t, s, 0, 75), KindNone, 0, 4, 1)
	// A different metric at the same now is a new evaluation.
	checkResult(t, mustEval(t, s, 0, 85), KindOut, 3, 4, 4)

	s2 := mustNew(t, cfg)
	checkResult(t, mustEval(t, s2, 0, 20), KindIn, 1, 3, 0)
	checkResult(t, mustEval(t, s2, 0, 20), KindNone, 0, 3, 0)
}

// TestDeterministicReplay: the same Evaluate sequence replays to identical
// actions, capacities and batches.
func TestDeterministicReplay(t *testing.T) {
	seq := [][2]int64{
		{0, 75}, {3, 85}, {5, 50}, {6, 20}, {8, 20},
		{10, 80}, {15, 90}, {20, 10}, {20, 10}, {25, 95},
	}
	run := func() ([]Result, []Batch, int64) {
		s := mustNew(t, exampleConfig())
		out := make([]Result, 0, len(seq))
		for _, e := range seq {
			r, err := s.Evaluate(e[0], e[1])
			if err != nil {
				t.Fatalf("Evaluate%v: %v", e, err)
			}
			out = append(out, r)
		}
		cap, batches, _, _, _, _, _, _ := s.Snapshot()
		return out, batches, cap
	}
	r1, b1, c1 := run()
	r2, b2, c2 := run()
	if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(b1, b2) || c1 != c2 {
		t.Fatalf("replay diverged: %v/%v/%d vs %v/%v/%d", r1, b1, c1, r2, b2, c2)
	}
}

// TestConcurrent hammers the controller from many goroutines; the final
// state must satisfy the capacity invariants (run with -race).
func TestConcurrent(t *testing.T) {
	cfg := exampleConfig()
	s := mustNew(t, cfg)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				now := int64(i)
				metric := int64((id*37 + i*13) % 120)
				_, _ = s.Evaluate(now, metric)
				_, _ = s.Evaluate(now, metric) // duplicate call
				s.Snapshot()
			}
		}(g)
	}
	wg.Wait()

	cap, batches, _, _, _, _, _, maxNow := s.Snapshot()
	if cap < cfg.Mn || cap > cfg.Mx {
		t.Fatalf("cap=%d outside [%d,%d]", cap, cfg.Mn, cfg.Mx)
	}
	eff := cap
	for _, b := range batches {
		eff += b.Count
		if b.ReadyAt <= maxNow {
			t.Fatalf("batch %+v ready at <= maxNow=%d", b, maxNow)
		}
	}
	if eff > cfg.Mx {
		t.Fatalf("eff=%d exceeds Mx=%d", eff, cfg.Mx)
	}
}
