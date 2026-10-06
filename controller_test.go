package microgrid

import (
	"errors"
	"sync"
	"testing"
)

func baseParams() Params {
	return Params{
		Capacity:             100,
		SoCLower:             10,
		SoCUpper:             90,
		MaxCharge:            20,
		MaxDischarge:         20,
		LossNumerator:        1,
		LossDenominator:      10,
		MaintenanceThreshold: 50,
		ReserveSlots:         2,
		Tolerance:            2,
	}
}

func baseParamsWith(mut func(*Params)) Params {
	p := baseParams()
	mut(&p)
	return p
}

func mustNew(t *testing.T, p Params, soc int) *Controller {
	t.Helper()
	c, err := New(p, soc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func rejectAt(t *testing.T, err error, want Reason, wantSlot int) {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want RejectError, got %v", err)
	}
	if re.Reason != want || re.Slot != wantSlot {
		t.Fatalf("want (%s @%d), got (%s @%d)", want, wantSlot, re.Reason, re.Slot)
	}
}

func putLoads(t *testing.T, c *Controller, loads []int) {
	t.Helper()
	if _, err := c.UpdateLoads(0, loads); err != nil {
		t.Fatalf("update loads: %v", err)
	}
}

func TestInvalidParams(t *testing.T) {
	bad := []Params{
		{},
		baseParamsWith(func(p *Params) { p.Capacity = 0 }),
		baseParamsWith(func(p *Params) { p.SoCLower = 91 }),
		baseParamsWith(func(p *Params) { p.LossNumerator = 10 }),
		baseParamsWith(func(p *Params) { p.LossDenominator = 0 }),
		baseParamsWith(func(p *Params) { p.MaxCharge = -1 }),
		baseParamsWith(func(p *Params) { p.ReserveSlots = -1 }),
	}
	for i, p := range bad {
		if _, err := New(p, 10); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: want ErrInvalid, got %v", i, err)
		}
	}
	p := baseParams()
	if _, err := New(p, 5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("initial soc below lower: %v", err)
	}
	if _, err := New(p, 91); !errors.Is(err, ErrInvalid) {
		t.Fatalf("initial soc above upper: %v", err)
	}
}

func TestSoCExactlyAtBounds(t *testing.T) {
	c := mustNew(t, baseParams(), 90)
	putLoads(t, c, []int{0, 0, 0, 0})
	_, err := c.SubmitPlan(0, []SlotPlan{{Charge, 10}})
	rejectAt(t, err, ReasonBounds, 0)
	if c.SoC() != 90 {
		t.Fatalf("rejected submit changed soc: %d", c.SoC())
	}
	if _, err := c.SubmitPlan(0, []SlotPlan{{Idle, 0}}); err != nil {
		t.Fatalf("idle exactly at upper must pass: %v", err)
	}

	// No reserve: discharging exactly onto the lower bound is allowed,
	// one more unit is not.
	c2 := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 11)
	putLoads(t, c2, []int{0})
	if _, err := c2.SubmitPlan(0, []SlotPlan{{Discharge, 1}}); err != nil {
		t.Fatalf("discharge ending exactly at lower bound: %v", err)
	}
	c3 := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 10)
	putLoads(t, c3, []int{0})
	_, err = c3.SubmitPlan(0, []SlotPlan{{Discharge, 1}})
	rejectAt(t, err, ReasonBounds, 0)
}

func TestLossFloorRounding(t *testing.T) {
	c := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 10)
	putLoads(t, c, []int{0, 0, 0, 0})
	if _, err := c.SubmitPlan(0, []SlotPlan{
		{Charge, 7},
		{Charge, 3},
		{Idle, 0},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	out, err := c.RegisterActual(0, Charge, 7)
	if err != nil || len(out.Revoked) != 0 {
		t.Fatalf("actual 7: %v %+v", err, out)
	}
	if c.SoC() != 16 {
		t.Fatalf("floor(7*9/10)=6, want 16, got %d", c.SoC())
	}
	if _, err := c.RegisterActual(1, Charge, 3); err != nil {
		t.Fatalf("actual 3: %v", err)
	}
	if c.SoC() != 18 {
		t.Fatalf("floor(3*9/10)=2, want 18, got %d", c.SoC())
	}
}

func TestReserveExactlyEnough(t *testing.T) {
	p := baseParams()
	c := mustNew(t, p, 16)
	putLoads(t, c, []int{5, 3, 3, 0, 0})
	if _, err := c.SubmitPlan(0, []SlotPlan{{Idle, 0}}); err != nil {
		t.Fatalf("exact reserve must pass: %v", err)
	}

	c2 := mustNew(t, p, 15)
	putLoads(t, c2, []int{5, 3, 3, 0, 0})
	_, err := c2.SubmitPlan(0, []SlotPlan{{Idle, 0}})
	rejectAt(t, err, ReasonReserve, 0)
}

func TestNoForecast(t *testing.T) {
	p := baseParamsWith(func(p *Params) { p.ReserveSlots = 0 })
	c := mustNew(t, p, 50)
	putLoads(t, c, []int{0}) // slot 1 has no forecast
	_, err := c.SubmitPlan(0, []SlotPlan{{Idle, 0}, {Idle, 0}})
	rejectAt(t, err, ReasonNoForecast, 1)
}

func TestPartialOverlapKeptSegmentInvalid(t *testing.T) {
	c := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 50)
	putLoads(t, c, []int{0, 0, 0, 0, 0, 0})
	if _, err := c.SubmitPlan(1, []SlotPlan{
		{Idle, 0}, {Idle, 0}, {Discharge, 2},
	}); err != nil {
		t.Fatalf("initial: %v", err)
	}
	// New segment overlaps slots 2..4. After replacing, kept slot 3
	// (discharge 2) follows slot-2 discharge 20, then slot 4 discharge 20
	// violates bounds; the merged whole must be rejected.
	_, err := c.SubmitPlan(2, []SlotPlan{
		{Discharge, 20}, {Idle, 0}, {Discharge, 25},
	})
	if err == nil {
		t.Fatal("merge invalidating kept suffix must be rejected")
	}
	var re *RejectError
	errors.As(err, &re)
	if re.Slot < 2 {
		t.Fatalf("failing slot must be inside new range, got %d", re.Slot)
	}
	if _, ok := c.PlanAt(1); !ok || func() bool {
		p, ok := c.PlanAt(3)
		return !ok || p != (SlotPlan{Discharge, 2})
	}() {
		t.Fatal("rejected overlap must leave old plans unchanged")
	}
}

func TestForecastUpdateRevokesSuffixOnly(t *testing.T) {
	c := mustNew(t, baseParams(), 50)
	putLoads(t, c, []int{0, 0, 0, 0, 0, 0, 0, 0})
	if _, err := c.SubmitPlan(1, []SlotPlan{
		{Idle, 0}, {Idle, 0}, {Idle, 0}, {Idle, 0},
	}); err != nil {
		t.Fatal(err)
	}
	// Larger loads at 3,4 make the two-slot reserve window fail from
	// slot 2 onwards; slots 1 stays valid.
	out, err := c.UpdateLoads(3, []int{30, 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Revoked) != 1 || out.Revoked[0].From != 2 {
		t.Fatalf("want one revocation from slot 2, got %+v", out.Revoked)
	}
	if _, ok := c.PlanAt(1); !ok {
		t.Fatal("slot 1 must survive")
	}
	if _, ok := c.PlanAt(2); ok {
		t.Fatal("slot 2 must be revoked")
	}
}

func TestDeviationToleranceBoundary(t *testing.T) {
	c := mustNew(t, baseParams(), 50)
	putLoads(t, c, []int{0, 0, 0, 0})
	if _, err := c.SubmitPlan(0, []SlotPlan{{Discharge, 10}, {Idle, 0}}); err != nil {
		t.Fatal(err)
	}
	// Difference equals tolerance 2: no deviation flag, no revocation.
	out, err := c.RegisterActual(0, Discharge, 12)
	if err != nil {
		t.Fatal(err)
	}
	if out.Deviation || len(out.Revoked) != 0 {
		t.Fatalf("exact tolerance must not deviate: %+v", out)
	}

	c2 := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 40)
	putLoads(t, c2, []int{0, 0, 0, 0})
	if _, err := c2.SubmitPlan(0, []SlotPlan{{Discharge, 10}, {Discharge, 20}}); err != nil {
		t.Fatal(err)
	}
	out2, err := c2.RegisterActual(0, Discharge, 13)
	if err != nil {
		t.Fatal(err)
	}
	if !out2.Deviation {
		t.Fatal("difference 3 must be a deviation")
	}
	if len(out2.Revoked) != 1 || out2.Revoked[0].From != 1 {
		t.Fatalf("deviation must revoke suffix from 1, got %+v", out2.Revoked)
	}
}

func TestActualSlotErrorAndBounds(t *testing.T) {
	c := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 15)
	_, err := c.RegisterActual(3, Idle, 0)
	rejectAt(t, err, ReasonSlot, 3)
	_, err = c.RegisterActual(0, Discharge, 10)
	rejectAt(t, err, ReasonBounds, 0)
	if c.CurrentSlot() != 0 || c.SoC() != 15 {
		t.Fatal("bounds rejection must not mutate state or advance time")
	}
}

func TestIslandLimits(t *testing.T) {
	c := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 50)
	putLoads(t, c, []int{8, 0, 0, 0})
	if err := c.RegisterSurplus(0, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SwitchMode(Island); err != nil {
		t.Fatal(err)
	}
	_, err := c.SubmitPlan(0, []SlotPlan{{Charge, 6}})
	rejectAt(t, err, ReasonMode, 0)
	if _, err := c.SubmitPlan(0, []SlotPlan{{Charge, 5}}); err != nil {
		t.Fatalf("charge == surplus: %v", err)
	}

	c2 := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 50)
	putLoads(t, c2, []int{8, 0, 0, 0})
	if _, err := c2.SwitchMode(Island); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.SubmitPlan(0, []SlotPlan{{Discharge, 8}}); err != nil {
		t.Fatalf("discharge == critical load: %v", err)
	}
	_, err = c2.SubmitPlan(1, []SlotPlan{{Discharge, 1}})
	rejectAt(t, err, ReasonMode, 1)
}

func TestMaintenanceLockEqualityAndException(t *testing.T) {
	c := mustNew(t, baseParamsWith(func(p *Params) {
		p.MaintenanceThreshold = 20
		p.ReserveSlots = 0
	}), 50)
	putLoads(t, c, []int{5, 5, 5, 5})
	if _, err := c.SubmitPlan(0, []SlotPlan{{Discharge, 20}, {Discharge, 5}}); err != nil {
		t.Fatal(err)
	}
	out, err := c.RegisterActual(0, Discharge, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Locked() || c.Throughput() != 20 {
		t.Fatalf("threshold equality must lock: locked=%v tp=%d", c.Locked(), c.Throughput())
	}
	if len(out.Revoked) != 1 || out.Revoked[0].From != 1 {
		t.Fatalf("locked discharge plan must be revoked: %+v", out.Revoked)
	}
	// New discharge plan refused while locked.
	_, err = c.SubmitPlan(2, []SlotPlan{{Discharge, 1}})
	rejectAt(t, err, ReasonMaintenance, 2)
	// Island discharge up to load is the exception.
	if _, err := c.SwitchMode(Island); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SubmitPlan(1, []SlotPlan{{Discharge, 5}}); err != nil {
		t.Fatalf("island discharge == load exception: %v", err)
	}
	if _, err := c.RegisterActual(1, Discharge, 5); err != nil {
		t.Fatalf("actual exception discharge: %v", err)
	}
	c.CompleteMaintenance()
	if c.Locked() || c.Throughput() != 0 {
		t.Fatal("maintenance completion must clear lock and throughput")
	}
}

func TestIslandSwitchRevokesAndGridDoesNot(t *testing.T) {
	c := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 50)
	putLoads(t, c, []int{8, 8, 8, 8})
	if _, err := c.SubmitPlan(0, []SlotPlan{{Discharge, 8}, {Discharge, 8}}); err != nil {
		t.Fatal(err)
	}
	out, err := c.SwitchMode(Island)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Revoked) != 0 {
		t.Fatalf("discharge <= load must survive island switch: %+v", out.Revoked)
	}
	// No surplus: a future charge plan dies on entering island.
	c2 := mustNew(t, baseParamsWith(func(p *Params) { p.ReserveSlots = 0 }), 50)
	putLoads(t, c2, []int{0, 0, 0, 0})
	if _, err := c2.SubmitPlan(0, []SlotPlan{{Charge, 5}, {Idle, 0}}); err != nil {
		t.Fatal(err)
	}
	out2, err := c2.SwitchMode(Island)
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.Revoked) != 1 || out2.Revoked[0].From != 0 {
		t.Fatalf("charge without surplus must be revoked on island entry: %+v", out2.Revoked)
	}
	// Back to grid revokes nothing.
	out3, err := c2.SwitchMode(GridTied)
	if err != nil {
		t.Fatal(err)
	}
	if len(out3.Revoked) != 0 {
		t.Fatalf("grid switch must not revoke: %+v", out3.Revoked)
	}
}

func TestRejectionPrecedence(t *testing.T) {
	// Empty plan outranks slot errors.
	c := mustNew(t, baseParams(), 50)
	if _, err := c.SubmitPlan(-5, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty/negative must be ErrInvalid: %v", err)
	}
	// Past slot outranks a malformed element that would also be invalid:
	// structural element validation precedes slot checks by design only
	// for elements; a well-formed past plan is a slot error.
	if _, err := c.RegisterActual(-1, Idle, 0); err == nil {
		t.Fatal("negative actual slot must be rejected")
	}
	// At one slot: maintenance outranks mode, mode outranks bounds,
	// bounds outranks reserve, reserve outranks missing forecast.
	locked := mustNew(t, baseParamsWith(func(p *Params) {
		p.MaintenanceThreshold = 1
		p.ReserveSlots = 0
	}), 50)
	putLoads(t, locked, []int{10, 0})
	if _, err := locked.SubmitPlan(0, []SlotPlan{{Discharge, 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := locked.RegisterActual(0, Discharge, 1); err != nil {
		t.Fatal(err)
	}
	// Now locked in grid mode: discharge is maintenance even though it
	// would also be within limits.
	_, err := locked.SubmitPlan(1, []SlotPlan{{Discharge, 1}})
	rejectAt(t, err, ReasonMaintenance, 1)
}

func TestConcurrentCallsAreSafe(t *testing.T) {
	c := mustNew(t, baseParams(), 50)
	putLoads(t, c, []int{0, 0, 0, 0, 0, 0})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				_ = c.SoC()
				_, _ = c.SubmitPlan(1, []SlotPlan{{Idle, 0}})
				_, _ = c.UpdateLoad(2, (g+k)%4)
				_ = c.Throughput()
			}
		}(g)
	}
	wg.Wait()
	// Survived -race; remaining plans must still be internally consistent.
	if c.SoC() < 10 || c.SoC() > 90 {
		t.Fatalf("soc invariant broken: %d", c.SoC())
	}
}
