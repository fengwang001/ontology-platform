package ontology

import (
	"errors"
	"sync"
	"testing"
)

func newExample(t *testing.T) *PageAllocator {
	t.Helper()
	a, err := NewPageAllocator(3, []int64{100, 300, 600}, []int64{8, 8, 1}, 100)
	if err != nil {
		t.Fatalf("NewPageAllocator() error = %v", err)
	}
	return a
}

func requireAlloc(t *testing.T, a *PageAllocator, preferred int, pages int64, gfp GFP, want int) {
	t.Helper()
	got, err := a.Alloc(preferred, pages, gfp)
	if err != nil || got != want {
		t.Fatalf("Alloc(%d, %d, %d) = (%d, %v), want %d", preferred, pages, gfp, got, err, want)
	}
}

func requireErr(t *testing.T, a *PageAllocator, want error) {
	t.Helper()
	if got := a.Stats(); got.Fallbacks != 0 || got.Wakes != 0 {
		t.Fatalf("Stats after rejected operation = %+v", got)
	}
}

func TestWatermarkAndReserveDerivation(t *testing.T) {
	a := newExample(t)

	wantMin := []int64{10, 30, 60}
	wantLow := []int64{12, 37, 75}
	wantHigh := []int64{15, 45, 90}
	wantAtomic := []int64{5, 15, 30}
	for z := 0; z < 3; z++ {
		if got := a.min[z]; got != wantMin[z] {
			t.Fatalf("min[%d] = %d, want %d", z, got, wantMin[z])
		}
		if got := a.low[z]; got != wantLow[z] {
			t.Fatalf("low[%d] = %d, want %d", z, got, wantLow[z])
		}
		if got := a.high[z]; got != wantHigh[z] {
			t.Fatalf("high[%d] = %d, want %d", z, got, wantHigh[z])
		}
		if got := a.atomic[z]; got != wantAtomic[z] {
			t.Fatalf("amin[%d] = %d, want %d", z, got, wantAtomic[z])
		}
	}

	reserves := []struct {
		zone      int
		preferred int
		want      int64
	}{
		{0, 1, 37},
		{0, 2, 112},
		{1, 2, 75},
		{2, 0, 0},
		{1, 1, 0},
	}
	for _, tc := range reserves {
		if got := a.reserve[tc.zone][tc.preferred]; got != tc.want {
			t.Fatalf("reserve[%d][%d] = %d, want %d", tc.zone, tc.preferred, got, tc.want)
		}
	}
}

func TestWatermarkFloorRounding(t *testing.T) {
	a, err := NewPageAllocator(3, []int64{5, 5, 5}, []int64{1, 1, 1}, 11)
	if err != nil {
		t.Fatalf("NewPageAllocator() error = %v", err)
	}

	if got := a.min[0]; got != 3 {
		t.Fatalf("floor min = %d, want 3", got)
	}
	if got := a.low[0]; got != 3 {
		t.Fatalf("low with remainder = %d, want 3", got)
	}
	if got := a.high[0]; got != 4 {
		t.Fatalf("high with remainder = %d, want 4", got)
	}
	if got := a.atomic[0]; got != 2 {
		t.Fatalf("amin with remainder = %d, want 2", got)
	}
	if got := a.step[0]; got != 1 {
		t.Fatalf("step = %d, want 1", got)
	}
	if got := a.cap[0]; got != 2 {
		t.Fatalf("cap = %d, want 2", got)
	}
}

func TestSpecifiedScenario(t *testing.T) {
	a := newExample(t)

	requireAlloc(t, a, 2, 510, NORMAL, 2)
	requireAlloc(t, a, 2, 10, NORMAL, 2)
	requireAlloc(t, a, 2, 10, NORMAL, 1)
	if got, want := a.boost[2], int64(22); got != want {
		t.Fatalf("boost[2] = %d, want %d", got, want)
	}
	requireAlloc(t, a, 1, 180, NORMAL, 1)

	requireAlloc(t, a, 2, 10, NORMAL, 2)
	if !a.kswapd[2] || a.wakes != 1 {
		t.Fatalf("kswapd = %v, wakes = %d", a.kswapd[2], a.wakes)
	}
	if got := a.free[2]; got != 70 {
		t.Fatalf("F[2] = %d, want 70", got)
	}

	_, err := a.Alloc(2, 10, NORMAL)
	if !errors.Is(err, ErrWouldReclaim) {
		t.Fatalf("exact min alloc error = %v, want ErrWouldReclaim", err)
	}

	requireAlloc(t, a, 2, 10, ATOMIC, 2)
	if got := a.free[2]; got != 60 {
		t.Fatalf("F[2] = %d, want 60", got)
	}

	if err := a.Free(2, 40); err != nil {
		t.Fatalf("Free() error = %v", err)
	}
	if got := a.kswapd[2]; !got {
		t.Fatal("kswapd[2] slept at exactly high+boost")
	}
	if got := a.boost[2]; got != 22 {
		t.Fatalf("boost[2] = %d, want 22", got)
	}
	if err := a.Free(2, 13); err != nil {
		t.Fatalf("Free() error = %v", err)
	}
	if got := a.kswapd[2]; got {
		t.Fatal("kswapd[2] still awake one page above high+boost")
	}
	if got := a.boost[2]; got != 0 {
		t.Fatalf("boost[2] = %d, want 0", got)
	}

	requireAlloc(t, a, 2, 113, EMERGENCY, 2)
	if got := a.free[2]; got != 0 {
		t.Fatalf("F[2] = %d, want 0", got)
	}
	stats := a.Stats()
	wantAllocs := []int64{0, 2, 5}
	for z := range wantAllocs {
		if stats.Allocations[z] != wantAllocs[z] {
			t.Fatalf("allocations[%d] = %d, want %d; stats = %+v", z, stats.Allocations[z], wantAllocs[z], stats)
		}
	}
	if stats.Fallbacks != 1 || stats.Wakes != 1 {
		t.Fatalf("stats = %+v, want one fallback and one wake", stats)
	}
}

func TestSecondPassWakeAndNormalMinBoundary(t *testing.T) {
	a := newExample(t)

	a.free[2] = 70
	a.free[1] = 100
	a.free[0] = 90

	requireAlloc(t, a, 2, 9, NORMAL, 2)
	if !a.kswapd[2] || a.wakes != 1 {
		t.Fatalf("kswapd = %v, wakes = %d", a.kswapd[2], a.wakes)
	}
	if got := a.free[2]; got != 61 {
		t.Fatalf("F[2] = %d, want 61", got)
	}

	_, err := a.Alloc(2, 1, NORMAL)
	if !errors.Is(err, ErrWouldReclaim) {
		t.Fatalf("Alloc() error = %v, want ErrWouldReclaim", err)
	}
	if a.wakes != 1 {
		t.Fatalf("wakes = %d, want 1", a.wakes)
	}
	if got := a.free[2]; got != 61 {
		t.Fatalf("failed alloc changed F[2] to %d", got)
	}
}

func TestAtomicUsesAtomicWatermark(t *testing.T) {
	a := newExample(t)
	a.free[2] = 60
	a.free[1] = 100
	a.free[0] = 90

	requireAlloc(t, a, 2, 29, ATOMIC, 2)
	if got := a.free[2]; got != 31 {
		t.Fatalf("F[2] = %d, want 31", got)
	}
	if !a.kswapd[2] || a.wakes != 1 {
		t.Fatalf("kswapd = %v, wakes = %d", a.kswapd[2], a.wakes)
	}

	a.free[1] = 0
	a.free[0] = 0
	_, err := a.Alloc(2, 1, ATOMIC)
	if !errors.Is(err, ErrNoMemory) {
		t.Fatalf("ATOMIC error = %v, want ErrNoMemory", err)
	}
}

func TestFallbackBoostCapAndEarlySecondPass(t *testing.T) {
	a := newExample(t)
	requireAlloc(t, a, 2, 510, NORMAL, 2)
	requireAlloc(t, a, 2, 10, NORMAL, 2)

	for _, wantBoost := range []int64{22, 44, 45} {
		requireAlloc(t, a, 2, 10, NORMAL, 1)
		if got := a.boost[2]; got != wantBoost {
			t.Fatalf("boost[2] = %d, want %d", got, wantBoost)
		}
	}
	if got := a.Stats().Fallbacks; got != 3 {
		t.Fatalf("fallbacks = %d, want 3", got)
	}

	if err := a.SetTotalMin(0); err != nil {
		t.Fatalf("SetTotalMin() error = %v", err)
	}
	if got := a.boost[2]; got != 0 {
		t.Fatalf("boost after SetTotalMin = %d, want 0", got)
	}
	if got := a.step[2]; got != 1 {
		t.Fatalf("step at zero totalMin = %d, want 1", got)
	}
}

func TestEmergencyIgnoresWatermarks(t *testing.T) {
	a := newExample(t)
	for z := range a.free {
		a.free[z] = 0
	}
	a.free[1] = 7
	a.kswapd[2] = true

	requireAlloc(t, a, 2, 7, EMERGENCY, 1)
	if a.free[1] != 0 || !a.kswapd[2] || a.wakes != 0 {
		t.Fatalf("free=%v kswapd=%v wakes=%d", a.free, a.kswapd, a.wakes)
	}
	_, err := a.Alloc(2, 1, EMERGENCY)
	if !errors.Is(err, ErrNoMemory) {
		t.Fatalf("empty emergency error = %v, want ErrNoMemory", err)
	}
}

func TestFreeBoundariesAndErrors(t *testing.T) {
	a := newExample(t)
	requireAlloc(t, a, 0, 40, NORMAL, 0)

	if err := a.Free(0, 40); err != nil {
		t.Fatalf("Free to capacity error = %v", err)
	}
	err := a.Free(0, 1)
	if !errors.Is(err, ErrOverfree) {
		t.Fatalf("Free over capacity error = %v, want ErrOverfree", err)
	}
	if got := a.free[0]; got != 100 {
		t.Fatalf("rejected free changed F[0] to %d", got)
	}

	_, err = a.Alloc(3, 1, NORMAL)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Alloc error = %v", err)
	}
	err = a.Free(-1, 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Free error = %v", err)
	}
	err = a.SetTotalMin(a.totalM + 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid SetTotalMin error = %v", err)
	}
	requireErr(t, a, ErrInvalidArgument)
}

func TestCheckLimit(t *testing.T) {
	a := newExample(t)
	_, _ = a.Alloc(2, 1_000_000_000, NORMAL)
	if got := a.Checks(); got > 2*int64(a.zones) {
		t.Fatalf("checks = %d, exceeds %d", got, 2*a.zones)
	}
}

func TestInvalidConfigurations(t *testing.T) {
	cases := []struct {
		name     string
		zones    int
		managed  []int64
		ratios   []int64
		totalMin int64
	}{
		{"zero zones", 0, nil, nil, 0},
		{"too many zones", 5, make([]int64, 5), make([]int64, 5), 0},
		{"zero managed page", 1, []int64{0}, []int64{1}, 0},
		{"managed page too large", 1, []int64{1_000_000_001}, []int64{1}, 0},
		{"zero ratio", 1, []int64{1}, []int64{0}, 0},
		{"ratio too large", 1, []int64{1}, []int64{1001}, 0},
		{"length mismatch", 2, []int64{1}, []int64{1, 1}, 0},
		{"totalMin above total", 1, []int64{1}, []int64{1}, 2},
		{"negative totalMin", 1, []int64{1}, []int64{1}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPageAllocator(tc.zones, tc.managed, tc.ratios, tc.totalMin)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewPageAllocator() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestExactWatermarkAndReserveBoundary(t *testing.T) {
	a := newExample(t)

	a.free[2] = 85
	a.free[1] = 0
	a.free[0] = 0
	requireAlloc(t, a, 2, 10, NORMAL, 2)
	if got := a.free[2]; got != 75 {
		t.Fatalf("F[2] after second-pass alloc = %d, want 75", got)
	}
	if !a.kswapd[2] || a.wakes != 1 {
		t.Fatalf("kswapd=%v wakes=%d, want awake once", a.kswapd[2], a.wakes)
	}

	a.free[1] = 0
	a.free[0] = 0
	_, err := a.Alloc(2, 15, NORMAL)
	if !errors.Is(err, ErrWouldReclaim) {
		t.Fatalf("F-n == min should fail second pass: %v", err)
	}

	a.free[2] = 86
	requireAlloc(t, a, 2, 10, NORMAL, 2)

	a.free[2] = 62
	a.free[1] = 115
	a.boost[2] = 0
	a.kswapd[2] = false
	a.wakes = 0
	requireAlloc(t, a, 2, 2, NORMAL, 1)

	a.free[2] = 62
	a.free[1] = 107
	a.boost[2] = 0
	a.kswapd[2] = false
	a.wakes = 0
	_, err = a.Alloc(2, 2, NORMAL)
	if !errors.Is(err, ErrWouldReclaim) {
		t.Fatalf("F-n == low+reserve should fail; error = %v", err)
	}
}

func TestSetTotalMinClampsBeforeClearing(t *testing.T) {
	a := newExample(t)
	a.free[2] = 70
	a.kswapd[2] = true
	a.wakes = 1
	a.boost[2] = 45

	a.free[2] = 67
	if err := a.SetTotalMin(50); err != nil {
		t.Fatalf("SetTotalMin(50) error = %v", err)
	}

	wantMin := int64(30)
	wantHigh := int64(45)
	if a.min[2] != wantMin || a.high[2] != wantHigh {
		t.Fatalf("marks after shrink = min:%d high:%d, want %d/%d", a.min[2], a.high[2], wantMin, wantHigh)
	}
	if got := a.boost[2]; got != 22 {
		t.Fatalf("boost after clamp = %d, want 22", got)
	}
	if !a.kswapd[2] {
		t.Fatal("kswapd should remain awake when F is not above high+boost")
	}

	a.free[2] = 68
	if err := a.SetTotalMin(48); err != nil {
		t.Fatalf("SetTotalMin(48) error = %v", err)
	}
	if a.kswapd[2] {
		t.Fatal("kswapd should clear once F is strictly above high+clamped boost")
	}
}

func TestKswapdSleepUsesOwnZoneOnly(t *testing.T) {
	a := newExample(t)
	a.free[1] = 0
	a.free[0] = 0
	a.free[2] = 70
	a.kswapd[2] = true

	if err := a.Free(2, 21); err != nil {
		t.Fatalf("Free() error = %v", err)
	}
	if a.kswapd[2] {
		t.Fatal("kswapd[2] should sleep based only on F[2]")
	}
}

func TestSetTotalMinIncreaseNeverWakesKswapd(t *testing.T) {
	a, err := NewPageAllocator(1, []int64{100}, []int64{1}, 0)
	if err != nil {
		t.Fatalf("NewPageAllocator() error = %v", err)
	}
	a.free[0] = 0
	if err := a.SetTotalMin(100); err != nil {
		t.Fatalf("SetTotalMin(100) error = %v", err)
	}
	if a.kswapd[0] || a.wakes != 0 {
		t.Fatalf("SetTotalMin set kswapd=%v wakes=%d", a.kswapd[0], a.wakes)
	}
}

func TestConcurrentOperations(t *testing.T) {
	a := newExample(t)
	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				preferred := worker % a.zones
				zone, err := a.Alloc(preferred, 1, GFP(i%3))
				if err == nil {
					_ = a.Free(zone, 1)
				}
				_ = a.SetTotalMin(int64(i % 101))
				_, _ = a.FreePages(preferred)
				_, _ = a.Boost(preferred)
				_ = a.Stats()
			}
		}(worker)
	}

	wg.Wait()
	for z := range a.managed {
		if a.free[z] < 0 || a.free[z] > a.managed[z] {
			t.Fatalf("invariant broken F[%d]=%d M=%d", z, a.free[z], a.managed[z])
		}
		if a.boost[z] < 0 || a.boost[z] > a.cap[z] {
			t.Fatalf("invariant broken boost[%d]=%d cap=%d", z, a.boost[z], a.cap[z])
		}
	}
}
