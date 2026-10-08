package dtc

import (
	"errors"
	"testing"
)

// testConfig uses a debounce where a single fail report judges failed
// and a single pass report judges passed, which keeps lifecycle tests
// short. Warm-up: rise >= 10 and final temp >= 70.
var testConfig = Config{
	DebounceRiseStep:  1,
	DebounceFailLimit: 1,
	DebounceFallStep:  1,
	DebouncePassLimit: 0,
	ConfirmCycles:     2,
	HealWarmupCycles:  2,
	AutoClearWarmups:  4,
	WarmupRise:        10,
	WarmupFinalTemp:   70,
}

// harness drives a Manager with monotone time/odometer and fails the
// test on unexpected errors.
type harness struct {
	t   *testing.T
	m   *Manager
	ts  int64
	odo int64
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return &harness{t: t, m: m, ts: 1000, odo: 10000}
}

func (h *harness) advance(dt, dodo int64) { h.ts += dt; h.odo += dodo }

func (h *harness) mustHandle(ev Event) {
	h.t.Helper()
	ev.Time, ev.Odometer = h.ts, h.odo
	if err := h.m.Handle(ev); err != nil {
		h.t.Fatalf("Handle(%+v) at t=%d: unexpected error: %v", ev, h.ts, err)
	}
	h.advance(1, 10)
}

func (h *harness) wantErr(ev Event, code ErrCode) {
	h.t.Helper()
	ev.Time, ev.Odometer = h.ts, h.odo
	err := h.m.Handle(ev)
	var derr *Error
	if !errors.As(err, &derr) || derr.Code != code {
		h.t.Fatalf("Handle(%+v): want error code %v, got %v", ev, code, err)
	}
	// Rejected events must not consume time/odometer: do not advance.
}

func (h *harness) on()            { h.mustHandle(Event{Kind: EvIgnitionOn}) }
func (h *harness) off()           { h.mustHandle(Event{Kind: EvIgnitionOff}) }
func (h *harness) fail(id string) { h.mustHandle(Event{Kind: EvMonitorResult, DTC: id}) }
func (h *harness) pass(id string) { h.mustHandle(Event{Kind: EvMonitorResult, DTC: id, Passed: true}) }
func (h *harness) env(speed, coolant int) {
	h.mustHandle(Event{Kind: EvEnvSample, Speed: speed, Coolant: coolant})
}

// failCycle runs one ignition cycle in which id is judged failed.
func (h *harness) failCycle(id string) { h.on(); h.fail(id); h.off() }

// passCycle runs one ignition cycle in which monitoring completes
// without failure; warmup controls whether it qualifies as warm-up.
func (h *harness) passCycle(id string, warmup bool) {
	h.on()
	if warmup {
		h.env(50, 20)
		h.env(60, 70) // rise 50 >= 10, final 70 >= 70
	}
	h.pass(id)
	h.off()
}

func (h *harness) query(id string) Snapshot {
	h.t.Helper()
	s, err := h.m.Query(id)
	if err != nil {
		h.t.Fatalf("Query(%q): %v", id, err)
	}
	return s
}

func mustRegister(t *testing.T, m *Manager, id string, sev int) {
	t.Helper()
	if err := m.Register(id, sev); err != nil {
		t.Fatalf("Register(%q, %d): %v", id, sev, err)
	}
}

func TestConfigValidation(t *testing.T) {
	base := testConfig
	cases := map[string]func(c *Config){
		"rise step":   func(c *Config) { c.DebounceRiseStep = 0 },
		"fail limit":  func(c *Config) { c.DebounceFailLimit = -1 },
		"fall step":   func(c *Config) { c.DebounceFallStep = 0 },
		"pass limit":  func(c *Config) { c.DebouncePassLimit = 1 },
		"confirm":     func(c *Config) { c.ConfirmCycles = 0 },
		"heal":        func(c *Config) { c.HealWarmupCycles = 0 },
		"auto<heal":   func(c *Config) { c.AutoClearWarmups = c.HealWarmupCycles - 1 },
		"warmup rise": func(c *Config) { c.WarmupRise = -1 },
	}
	for name, mutate := range cases {
		cfg := base
		mutate(&cfg)
		if _, err := NewManager(cfg); err == nil {
			t.Errorf("%s: expected ErrInvalidParam, got nil", name)
		} else {
			var derr *Error
			if !errors.As(err, &derr) || derr.Code != ErrInvalidParam {
				t.Errorf("%s: expected ErrInvalidParam, got %v", name, err)
			}
		}
	}
	if _, err := NewManager(base); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	h := newHarness(t, testConfig)
	if err := h.m.Register("P0001", 0); err == nil {
		t.Error("severity 0 accepted")
	}
	if err := h.m.Register("P0001", 4); err == nil {
		t.Error("severity 4 accepted")
	}
	if err := h.m.Register("", 1); err == nil {
		t.Error("empty id accepted")
	}
	mustRegister(t, h.m, "P0001", 2)
	if err := h.m.Register("P0001", 2); err == nil {
		t.Error("duplicate registration accepted")
	}
}

// TestDebounceExactLimits drives the debounce counter exactly onto both
// limits and checks clamping and the sticky judgment in between.
func TestDebounceExactLimits(t *testing.T) {
	cfg := testConfig
	cfg.DebounceRiseStep = 3
	cfg.DebounceFailLimit = 6
	cfg.DebounceFallStep = 2
	cfg.DebouncePassLimit = -2
	h := newHarness(t, cfg)
	mustRegister(t, h.m, "P0001", 1)

	h.on()
	h.fail("P0001") // value 3: between the lines, no judgment
	if got := h.query("P0001").Judgment; got != JudgmentNone {
		t.Fatalf("after 1 fail: judgment = %v, want none", got)
	}
	h.fail("P0001") // value 6 == fail limit: judged failed
	if got := h.query("P0001").Judgment; got != JudgmentFail {
		t.Fatalf("at fail limit: judgment = %v, want fail", got)
	}
	h.fail("P0001") // clamped at 6, still failed
	if got := h.query("P0001").Judgment; got != JudgmentFail {
		t.Fatalf("above fail limit: judgment = %v, want fail", got)
	}
	h.pass("P0001") // value 4: previous judgment (fail) kept
	if got := h.query("P0001").Judgment; got != JudgmentFail {
		t.Fatalf("between lines: judgment = %v, want fail (sticky)", got)
	}
	h.pass("P0001") // value 2
	h.pass("P0001") // value 0
	if got := h.query("P0001").Judgment; got != JudgmentFail {
		t.Fatalf("at 0 above pass limit -2: judgment = %v, want fail (sticky)", got)
	}
	h.pass("P0001") // value -2 == pass limit: judged passed
	if got := h.query("P0001").Judgment; got != JudgmentPass {
		t.Fatalf("at pass limit: judgment = %v, want pass", got)
	}
	h.pass("P0001") // clamped at -2, still passed
	if got := h.query("P0001").Judgment; got != JudgmentPass {
		t.Fatalf("below pass limit: judgment = %v, want pass", got)
	}
	h.off()
	// Ignition off clears the debounce value and the judgment.
	if got := h.query("P0001").Judgment; got != JudgmentNone {
		t.Fatalf("after ignition off: judgment = %v, want none", got)
	}
	h.on()
	if got := h.query("P0001").Judgment; got != JudgmentNone {
		t.Fatalf("new cycle: judgment = %v, want none", got)
	}
	h.fail("P0001") // counter restarted at 0 -> value 3, no judgment yet
	if got := h.query("P0001").Judgment; got != JudgmentNone {
		t.Fatalf("new cycle after 1 fail: judgment = %v, want none", got)
	}
	h.off()
}

// TestFailPassFailSameCycle: the first failed judgment of a cycle sets
// pending and counts one occurrence; a later pass->fail transition in
// the same cycle does not count again.
func TestFailPassFailSameCycle(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "P0001", 1)

	h.on()
	h.fail("P0001")
	h.pass("P0001")
	h.fail("P0001")
	s := h.query("P0001")
	if !s.Pending || s.Judgment != JudgmentFail {
		t.Fatalf("want pending+fail judgment, got %+v", s)
	}
	if s.Occurrences != 1 {
		t.Fatalf("occurrences = %d, want 1 (same cycle counts once)", s.Occurrences)
	}
	h.off()

	// A failed cycle in a later cycle counts one more occurrence.
	h.failCycle("P0001")
	if got := h.query("P0001").Occurrences; got != 2 {
		t.Fatalf("occurrences = %d, want 2 (one per failed cycle)", got)
	}
}

// TestMonitorIncompleteCycle: a cycle with neither a failed nor a
// passed judgment leaves the consecutive-failed-cycle count untouched.
func TestMonitorIncompleteCycle(t *testing.T) {
	cfg := testConfig
	cfg.DebounceRiseStep = 1
	cfg.DebounceFailLimit = 2 // a single fail report does not judge
	h := newHarness(t, cfg)
	mustRegister(t, h.m, "P0001", 1)

	h.failCycle("P0001") // 2 reports? no: one report, value 1 < 2 -> no judgment
	if got := h.query("P0001").ConsecFailCycles; got != 0 {
		t.Fatalf("consec = %d, want 0 (no judgment happened)", got)
	}

	// Produce one real failed cycle: two fail reports reach the limit.
	h.on()
	h.fail("P0001")
	h.fail("P0001")
	h.off()
	if got := h.query("P0001").ConsecFailCycles; got != 1 {
		t.Fatalf("consec = %d, want 1", got)
	}

	// Incomplete cycle: one fail report below the limit, no judgment.
	h.failCycle("P0001")
	if got := h.query("P0001").ConsecFailCycles; got != 1 {
		t.Fatalf("consec = %d, want 1 (incomplete cycle keeps count)", got)
	}

	// Empty cycle: no reports at all.
	h.on()
	h.off()
	if got := h.query("P0001").ConsecFailCycles; got != 1 {
		t.Fatalf("consec = %d, want 1 (empty cycle keeps count)", got)
	}

	// Completed cycle without failure resets the count.
	h.passCycle("P0001", false)
	if got := h.query("P0001").ConsecFailCycles; got != 0 {
		t.Fatalf("consec = %d, want 0 (completed clean cycle resets)", got)
	}
}

// TestConfirmExactAndOneLess: with ConfirmCycles = 2 the DTC confirms at
// the ignition off of the second consecutive failed cycle, not earlier.
func TestConfirmExactAndOneLess(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "P0001", 1)

	h.failCycle("P0001") // 1st failed cycle: one less than required
	s := h.query("P0001")
	if s.Confirmed || !s.Pending {
		t.Fatalf("after 1 failed cycle: confirmed=%v pending=%v, want pending only",
			s.Confirmed, s.Pending)
	}
	if s.ConsecFailCycles != 1 {
		t.Fatalf("consec = %d, want 1", s.ConsecFailCycles)
	}

	h.failCycle("P0001") // 2nd consecutive failed cycle: confirm at ignition off
	s = h.query("P0001")
	if !s.Confirmed || !s.Pending {
		t.Fatalf("after 2 failed cycles: confirmed=%v pending=%v, want both (pending kept)",
			s.Confirmed, s.Pending)
	}

	// A completed clean cycle resets the run; one new failed cycle is
	// again one less than required (already confirmed stays confirmed).
	h.passCycle("P0001", false)
	h.failCycle("P0001")
	if got := h.query("P0001").ConsecFailCycles; got != 1 {
		t.Fatalf("consec = %d, want 1 after reset+1 failed cycle", got)
	}
}

// confirmNow is a helper that drives a DTC into the confirmed state
// (testConfig: ConfirmCycles = 2).
func confirmNow(h *harness, id string) {
	h.failCycle(id)
	h.failCycle(id)
}

// TestHealExactAndOneLess: with HealWarmupCycles = 2 the DTC heals at
// the ignition off of the second fault-free warm-up cycle.
func TestHealExactAndOneLess(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "P0001", 1)
	confirmNow(h, "P0001")

	h.passCycle("P0001", true) // 1st fault-free warm-up: one less than required
	s := h.query("P0001")
	if !s.Confirmed || s.Healed || s.FaultFreeWarmups != 1 {
		t.Fatalf("after 1 warm-up: %+v, want still confirmed, warmups=1", s)
	}

	h.passCycle("P0001", true) // 2nd: heal at ignition off
	s = h.query("P0001")
	if s.Confirmed || s.Pending || !s.Healed {
		t.Fatalf("after 2 warm-ups: %+v, want healed (confirmed+pending cleared)", s)
	}
	if s.FaultFreeWarmups != 2 {
		t.Fatalf("faultFreeWarmups = %d, want 2 (kept as history)", s.FaultFreeWarmups)
	}
}

// TestHealCountingRules: only warm-up cycles with completed monitoring
// and no failure count; failures reset; non-warm-up cycles are neutral.
func TestHealCountingRules(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "P0001", 1)
	confirmNow(h, "P0001")

	h.passCycle("P0001", false) // clean but not a warm-up: no change
	if got := h.query("P0001").FaultFreeWarmups; got != 0 {
		t.Fatalf("non-warm-up cycle changed warmups to %d", got)
	}

	h.passCycle("P0001", true) // 1
	h.on()                     // warm-up cycle without monitoring: no change
	h.env(50, 20)
	h.env(60, 70)
	h.off()
	if got := h.query("P0001").FaultFreeWarmups; got != 1 {
		t.Fatalf("warm-up without monitoring changed warmups to %d, want 1", got)
	}

	h.failCycle("P0001") // a failure resets the fault-free run
	if got := h.query("P0001").FaultFreeWarmups; got != 0 {
		t.Fatalf("failed cycle: warmups = %d, want 0", got)
	}
}

// TestWarmupExactThresholds: the coolant rise exactly equal to the
// configured rise qualifies; one degree less does not. The final
// temperature must also have been reached.
func TestWarmupExactThresholds(t *testing.T) {
	cfg := testConfig
	cfg.ConfirmCycles = 1
	cfg.HealWarmupCycles = 1
	h := newHarness(t, cfg)
	mustRegister(t, h.m, "P0001", 1)
	h.failCycle("P0001") // confirmed (ConfirmCycles = 1)

	// Rise 9 < 10: not a warm-up even though final temp reached.
	h.on()
	h.env(50, 61)
	h.env(50, 70)
	h.pass("P0001")
	h.off()
	if s := h.query("P0001"); s.Healed || s.FaultFreeWarmups != 0 {
		t.Fatalf("rise 9: %+v, want no warm-up credit", s)
	}

	// Rise exactly 10 and final temp exactly 70: warm-up.
	h.on()
	h.env(50, 60)
	h.env(50, 70)
	h.pass("P0001")
	h.off()
	if s := h.query("P0001"); !s.Healed || s.FaultFreeWarmups != 1 {
		t.Fatalf("rise 10 exact: %+v, want healed with warmups=1", s)
	}
}

// TestFreezeFrameSeverityRules: the first pending DTC captures the
// slot; equal severity does not replace, strictly higher does.
func TestFreezeFrameSeverityRules(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "LOW1", 1)
	mustRegister(t, h.m, "MID1", 2)
	mustRegister(t, h.m, "MID2", 2)
	mustRegister(t, h.m, "HIGH1", 3)

	h.on()
	h.env(88, 55) // environment captured by the first failure
	h.fail("MID1")
	h.off()
	ff := h.m.FreezeFrame()
	if !ff.Occupied || ff.Owner != "MID1" {
		t.Fatalf("slot = %+v, want owner MID1", ff)
	}
	if ff.Speed != 88 || ff.Coolant != 55 {
		t.Fatalf("frame = %+v, want speed 88 coolant 55", ff)
	}
	if !h.query("MID1").HasFreezeFrame {
		t.Fatal("MID1 should own the frame")
	}

	// Equal severity does not replace.
	h.failCycle("MID2")
	if got := h.m.FreezeFrame().Owner; got != "MID1" {
		t.Fatalf("equal severity replaced the slot: owner = %q", got)
	}
	if h.query("MID2").HasFreezeFrame {
		t.Fatal("MID2 must not own the frame")
	}

	// Lower severity does not replace either.
	h.failCycle("LOW1")
	if got := h.m.FreezeFrame().Owner; got != "MID1" {
		t.Fatalf("lower severity replaced the slot: owner = %q", got)
	}

	// Strictly higher severity replaces; the old owner loses the frame.
	h.on()
	h.env(30, 40)
	h.fail("HIGH1")
	h.off()
	ff = h.m.FreezeFrame()
	if ff.Owner != "HIGH1" || ff.Speed != 30 || ff.Coolant != 40 {
		t.Fatalf("slot = %+v, want HIGH1 with new env data", ff)
	}
	if h.query("MID1").HasFreezeFrame {
		t.Fatal("MID1 must have lost the frame")
	}
	if !h.query("HIGH1").HasFreezeFrame {
		t.Fatal("HIGH1 should own the frame")
	}
}

// TestFreezeFrameCapturedData: the frame stores the odometer of the
// failing report together with the latest environment sample.
func TestFreezeFrameCapturedData(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "P0001", 1)
	h.on()
	h.env(120, 90)
	h.advance(5, 300)
	h.fail("P0001")
	h.off()
	ff := h.m.FreezeFrame()
	// Each handled event advances the odometer by 10; advance() added 300.
	wantOdo := int64(10000) + 10 + 10 + 300
	if ff.Odometer != wantOdo {
		t.Fatalf("frame odometer = %d, want %d", ff.Odometer, wantOdo)
	}
	if ff.Speed != 120 || ff.Coolant != 90 {
		t.Fatalf("frame env = %d/%d, want 120/90", ff.Speed, ff.Coolant)
	}
}

// TestTesterClear: clear wipes all state, history and the freeze frame,
// records the odometer baseline, and is only allowed with ignition off.
func TestTesterClear(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "P0001", 3)
	confirmNow(h, "P0001")
	if !h.query("P0001").HasFreezeFrame {
		t.Fatal("precondition: P0001 owns the frame")
	}

	// Clear while ignition on is rejected and changes nothing.
	h.on()
	h.wantErr(Event{Kind: EvClear}, ErrStateNotAllowed)
	h.off()
	if !h.query("P0001").Confirmed {
		t.Fatal("rejected clear must not change state")
	}

	h.advance(0, 1234)
	baseline := h.odo
	h.mustHandle(Event{Kind: EvClear})

	s := h.query("P0001")
	if s.Pending || s.Confirmed || s.Healed || s.Occurrences != 0 ||
		s.ConsecFailCycles != 0 || s.FaultFreeWarmups != 0 || s.HasFreezeFrame {
		t.Fatalf("after clear: %+v, want fully reset", s)
	}
	if ff := h.m.FreezeFrame(); ff.Occupied {
		t.Fatalf("frame slot not released: %+v", ff)
	}
	if s.DistanceSinceClear != 0 {
		t.Fatalf("distance since clear = %d, want 0", s.DistanceSinceClear)
	}

	// Drive on: the distance is measured from the clear baseline.
	h.failCycle("P0001")
	// h.odo is the odometer of the *next* event; the manager tracks the
	// last accepted event, which is one step (10) behind.
	if got, want := h.query("P0001").DistanceSinceClear, h.odo-10-baseline; got != want {
		t.Fatalf("distance since clear = %d, want %d", got, want)
	}
	// The slot is free again, so the re-failing DTC captures it anew.
	if !h.query("P0001").HasFreezeFrame {
		t.Fatal("after clear the slot should be free for re-capture")
	}
}

// TestAutoClear: a healed DTC is fully erased once its fault-free
// warm-up total reaches the auto-clear threshold; the freeze frame slot
// is released and not handed over.
func TestAutoClear(t *testing.T) {
	cfg := testConfig
	cfg.ConfirmCycles = 1
	cfg.HealWarmupCycles = 1
	cfg.AutoClearWarmups = 3
	h := newHarness(t, cfg)
	mustRegister(t, h.m, "P0001", 2)
	mustRegister(t, h.m, "P0002", 1)

	h.failCycle("P0001") // confirmed, owns the slot
	h.passCycle("P0001", true)
	if s := h.query("P0001"); !s.Healed || s.FaultFreeWarmups != 1 {
		t.Fatalf("want healed with 1 warm-up, got %+v", s)
	}
	if !h.query("P0001").HasFreezeFrame {
		t.Fatal("healed DTC keeps the freeze frame until auto-clear")
	}

	h.passCycle("P0001", true) // 2: still history
	if got := h.query("P0001").FaultFreeWarmups; got != 2 {
		t.Fatalf("warmups = %d, want 2", got)
	}

	h.passCycle("P0001", true) // 3 == AutoClearWarmups: fully erased
	s := h.query("P0001")
	if s.Healed || s.Pending || s.Confirmed || s.FaultFreeWarmups != 0 || s.Occurrences != 0 {
		t.Fatalf("after auto-clear: %+v, want zero snapshot", s)
	}
	if ff := h.m.FreezeFrame(); ff.Occupied {
		t.Fatalf("slot not released on auto-clear: %+v", ff)
	}

	// The freed slot is not handed over automatically: P0002 fails and
	// captures it only because it enters pending while the slot is free.
	h.failCycle("P0002")
	if got := h.m.FreezeFrame().Owner; got != "P0002" {
		t.Fatalf("slot owner = %q, want P0002", got)
	}
}

// TestHealedRefail: a healed DTC that fails again counts a new
// occurrence, re-enters pending and restarts the consecutive failed
// cycle count from zero.
func TestHealedRefail(t *testing.T) {
	cfg := testConfig
	cfg.ConfirmCycles = 2
	cfg.HealWarmupCycles = 1
	cfg.AutoClearWarmups = 5
	h := newHarness(t, cfg)
	mustRegister(t, h.m, "P0001", 1)

	confirmNow(h, "P0001") // occurrences = 2
	h.passCycle("P0001", true)
	if s := h.query("P0001"); !s.Healed {
		t.Fatalf("precondition: want healed, got %+v", s)
	}

	h.on()
	h.fail("P0001")
	s := h.query("P0001")
	if !s.Pending || s.Healed || s.Confirmed {
		t.Fatalf("after re-fail: %+v, want pending, not healed/confirmed", s)
	}
	if s.Occurrences != 3 {
		t.Fatalf("occurrences = %d, want 3", s.Occurrences)
	}
	h.off()
	s = h.query("P0001")
	if s.ConsecFailCycles != 1 {
		t.Fatalf("consec = %d, want 1 (restarted from zero)", s.ConsecFailCycles)
	}
	if s.FaultFreeWarmups != 0 {
		t.Fatalf("warmups = %d, want 0 (reset by failure)", s.FaultFreeWarmups)
	}
	if s.Confirmed {
		t.Fatal("confirmed too early: need 2 consecutive failed cycles")
	}
	h.failCycle("P0001")
	if !h.query("P0001").Confirmed {
		t.Fatal("want confirmed again after 2 consecutive failed cycles")
	}
}

// TestErrorPriority: when an event violates several rules, the
// highest-priority error wins: invalid param > regression > bad order >
// unknown DTC > state not allowed.
func TestErrorPriority(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "P0001", 1)
	h.on()

	// Invalid param (negative time) + regression + bad order.
	bad := Event{Kind: EvMonitorResult, DTC: "GHOST", Time: -5, Odometer: h.odo - 1}
	h.wantErrAt(bad, ErrInvalidParam)

	// Regression (time) + unknown DTC: regression wins. Equal time is
	// legal, so step two ticks back.
	reg := Event{Kind: EvMonitorResult, DTC: "GHOST", Time: h.ts - 2, Odometer: h.odo}
	h.wantErrAt(reg, ErrRegression)
	regOdo := Event{Kind: EvMonitorResult, DTC: "GHOST", Time: h.ts, Odometer: h.odo - 20}
	h.wantErrAt(regOdo, ErrRegression)

	// Bad order (ignition on while on) beats nothing else here.
	h.wantErr(Event{Kind: EvIgnitionOn}, ErrBadSequence)
	// Bad order (env sample while off) — checked after off below.

	// Unknown DTC while order is fine.
	h.wantErr(Event{Kind: EvMonitorResult, DTC: "GHOST"}, ErrUnknownDTC)

	// State not allowed: clear with ignition on.
	h.wantErr(Event{Kind: EvClear}, ErrStateNotAllowed)

	h.off()
	// Monitor result while off: bad order beats unknown DTC.
	h.wantErr(Event{Kind: EvMonitorResult, DTC: "GHOST"}, ErrBadSequence)
	// Env sample while off.
	h.wantErr(Event{Kind: EvEnvSample, Speed: 10, Coolant: 20}, ErrBadSequence)
	// Ignition off while off.
	h.wantErr(Event{Kind: EvIgnitionOff}, ErrBadSequence)
	// Query of an unknown DTC.
	if _, err := h.m.Query("GHOST"); err == nil {
		t.Fatal("query of unknown DTC must fail")
	} else {
		var derr *Error
		if !errors.As(err, &derr) || derr.Code != ErrUnknownDTC {
			t.Fatalf("query error = %v, want ErrUnknownDTC", err)
		}
	}
}

// wantErrAt is like wantErr but uses the event's own time/odometer.
func (h *harness) wantErrAt(ev Event, code ErrCode) {
	h.t.Helper()
	err := h.m.Handle(ev)
	var derr *Error
	if !errors.As(err, &derr) || derr.Code != code {
		h.t.Fatalf("Handle(%+v): want error code %v, got %v", ev, code, err)
	}
}

// TestRejectedEventKeepsState: a rejected event changes nothing —
// including the accepted time/odometer watermark.
func TestRejectedEventKeepsState(t *testing.T) {
	h := newHarness(t, testConfig)
	mustRegister(t, h.m, "P0001", 1)
	h.on()
	h.fail("P0001")
	before := h.query("P0001")

	// Rejected: regression.
	h.wantErrAt(Event{Kind: EvMonitorResult, DTC: "P0001", Time: h.ts - 2, Odometer: h.odo},
		ErrRegression)
	// Rejected: unknown DTC.
	h.wantErr(Event{Kind: EvMonitorResult, DTC: "GHOST"}, ErrUnknownDTC)

	after := h.query("P0001")
	if before != after {
		t.Fatalf("state changed by rejected events:\nbefore %+v\nafter  %+v", before, after)
	}
	// The watermark did not move: an event at the current time is still
	// accepted (equal time/odometer is allowed).
	h.mustHandle(Event{Kind: EvMonitorResult, DTC: "P0001", Time: h.ts, Odometer: h.odo})
	h.off()
}

// TestConcurrentAccess hammers the manager from many goroutines; run
// with -race. Every goroutine either replays a deterministic valid
// script or only queries, so the final state must be exactly the one of
// the single scripted writer.
func TestConcurrentAccess(t *testing.T) {
	m, err := NewManager(testConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Register("P0001", 2); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					_, _ = m.Query("P0001")
					_ = m.FreezeFrame()
					_ = m.WorkUnits()
				}
			}
		}()
	}

	var ts, odo int64 = 0, 0
	step := func(ev Event) {
		ts++
		odo += 10
		ev.Time, ev.Odometer = ts, odo
		if err := m.Handle(ev); err != nil {
			t.Errorf("Handle(%+v): %v", ev, err)
		}
	}
	for i := 0; i < 200; i++ {
		step(Event{Kind: EvIgnitionOn})
		step(Event{Kind: EvMonitorResult, DTC: "P0001"})
		step(Event{Kind: EvIgnitionOff})
	}
	close(done)

	s, err := m.Query("P0001")
	if err != nil {
		t.Fatal(err)
	}
	if s.Occurrences != 200 || s.ConsecFailCycles != 200 || !s.Confirmed {
		t.Fatalf("final snapshot = %+v, want 200 occurrences/failed cycles, confirmed", s)
	}
}
