package pagealloc

import (
	"errors"
	"testing"
)

func TestMarksRoundingAndReserves(t *testing.T) {
	a, err := New(1, []int64{7}, []int64{8}, 3)
	if err != nil {
		t.Fatal(err)
	}
	marks, err := a.Marks(0)
	if err != nil {
		t.Fatal(err)
	}
	if marks != (Marks{Min: 3, Low: 3, High: 4, Atomic: 2}) {
		t.Fatalf("marks = %+v", marks)
	}

	a, err = New(3, []int64{100, 300, 600}, []int64{8, 8, 1}, 100)
	if err != nil {
		t.Fatal(err)
	}
	wantMarks := []Marks{
		{Min: 10, Low: 12, High: 15, Atomic: 5},
		{Min: 30, Low: 37, High: 45, Atomic: 15},
		{Min: 60, Low: 75, High: 90, Atomic: 30},
	}
	for zone, want := range wantMarks {
		got, marksErr := a.Marks(zone)
		if marksErr != nil {
			t.Fatal(marksErr)
		}
		if got != want {
			t.Fatalf("marks[%d] = %+v, want %+v", zone, got, want)
		}
	}

	wantReserves := map[[2]int]int64{
		{0, 0}: 0, {0, 1}: 37, {0, 2}: 112,
		{1, 1}: 0, {1, 2}: 75,
		{2, 0}: 0, {2, 2}: 0,
	}
	for coordinates, want := range wantReserves {
		got, reserveErr := a.Reserve(coordinates[0], coordinates[1])
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		if got != want {
			t.Fatalf("reserve%v = %d, want %d", coordinates, got, want)
		}
	}
}

func TestStrictPredicateAndWholePages(t *testing.T) {
	a, err := New(2, []int64{100, 100}, []int64{1000, 1000}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if zone, allocErr := a.Alloc(1, 1, NORMAL); allocErr != nil || zone != 1 {
		t.Fatalf("Alloc() = (%d, %v), want zone 1", zone, allocErr)
	}
	if _, allocErr := a.Alloc(1, 100, NORMAL); !errors.Is(allocErr, ErrWouldReclaim) {
		t.Fatalf("Alloc() error = %v, want ErrWouldReclaim", allocErr)
	}
	if got, _ := a.FreePages(1); got != 99 {
		t.Fatalf("free[1] = %d, want 99 after failed whole request", got)
	}
}

func TestFallbackReserveBoostAndWakeOnce(t *testing.T) {
	a, err := New(2, []int64{100, 100}, []int64{10, 10}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, allocErr := a.Alloc(1, 80, NORMAL); allocErr != nil {
		t.Fatal(allocErr)
	}
	if _, allocErr := a.Alloc(1, 7, NORMAL); allocErr != nil {
		t.Fatal(allocErr)
	}
	zone, allocErr := a.Alloc(1, 1, NORMAL)
	if allocErr != nil || zone != 0 {
		t.Fatalf("fallback = (%d, %v), want zone 0", zone, allocErr)
	}
	if boost, _ := a.Boost(1); boost != 3 {
		t.Fatalf("boost = %d, want 3", boost)
	}
	if awake, _ := a.Kswapd(1); awake {
		t.Fatal("successful fallback must not wake kswapd")
	}

	if _, allocErr := a.Alloc(0, 99, EMERGENCY); allocErr != nil {
		t.Fatal(allocErr)
	}
	if zone, allocErr := a.Alloc(1, 1, NORMAL); allocErr != nil || zone != 1 {
		t.Fatalf("second-pass allocation = (%d, %v), want zone 1", zone, allocErr)
	}
	if _, allocErr = a.Alloc(1, 2, NORMAL); !errors.Is(allocErr, ErrWouldReclaim) {
		t.Fatalf("repeated allocation error = %v, want ErrWouldReclaim", allocErr)
	}
	if wakes := a.Stats().Wakes; wakes != 1 {
		t.Fatalf("wakes = %d, want 1", wakes)
	}
}

func TestSecondPassUsesMinAndAtomicMarks(t *testing.T) {
	a, err := New(1, []int64{100}, []int64{8}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, allocErr := a.Alloc(0, 79, NORMAL); allocErr != nil {
		t.Fatal(allocErr)
	}
	if _, allocErr := a.Alloc(0, 1, NORMAL); !errors.Is(allocErr, ErrWouldReclaim) {
		t.Fatalf("NORMAL error = %v, want ErrWouldReclaim", allocErr)
	}
	zone, allocErr := a.Alloc(0, 1, ATOMIC)
	if allocErr != nil || zone != 0 {
		t.Fatalf("ATOMIC = (%d, %v), want zone 0", zone, allocErr)
	}
	if got, _ := a.FreePages(0); got != 20 {
		t.Fatalf("free = %d, want 20", got)
	}
}

func TestFreeSleepBoundaryAndEmergency(t *testing.T) {
	a, err := New(1, []int64{100}, []int64{8}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, allocErr := a.Alloc(0, 86, NORMAL); allocErr != nil {
		t.Fatal(allocErr)
	}
	if _, allocErr := a.Alloc(0, 4, NORMAL); !errors.Is(allocErr, ErrWouldReclaim) {
		t.Fatalf("boundary Alloc(4) error = %v, want ErrWouldReclaim", allocErr)
	}
	if err := a.Free(0, 1); err != nil {
		t.Fatal(err)
	}
	if awake, _ := a.Kswapd(0); !awake {
		t.Fatal("kswapd must remain awake exactly at high + boost")
	}
	if err := a.Free(0, 1); err != nil {
		t.Fatal(err)
	}
	if awake, _ := a.Kswapd(0); awake {
		t.Fatal("kswapd must sleep above high + boost")
	}
	if boost, _ := a.Boost(0); boost != 0 {
		t.Fatalf("boost = %d, want 0", boost)
	}

	emergency, err := New(1, []int64{100}, []int64{8}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if zone, allocErr := emergency.Alloc(0, 16, EMERGENCY); allocErr != nil || zone != 0 {
		t.Fatalf("EMERGENCY = (%d, %v), want zone 0", zone, allocErr)
	}
	if got, _ := emergency.FreePages(0); got != 84 {
		t.Fatalf("free = %d, want 84", got)
	}
	if zone, allocErr := emergency.Alloc(0, 84, EMERGENCY); allocErr != nil || zone != 0 {
		t.Fatalf("exhausting EMERGENCY = (%d, %v), want zone 0", zone, allocErr)
	}
	if got, _ := emergency.FreePages(0); got != 0 {
		t.Fatalf("free = %d, want 0", got)
	}
	if _, allocErr := emergency.Alloc(0, 1, EMERGENCY); !errors.Is(allocErr, ErrNoMemory) {
		t.Fatalf("EMERGENCY error = %v, want ErrNoMemory", allocErr)
	}
	if awake, _ := emergency.Kswapd(0); awake {
		t.Fatal("EMERGENCY touched kswapd")
	}
	if wakes := emergency.Stats().Wakes; wakes != 0 {
		t.Fatalf("wakes = %d, want 0", wakes)
	}
}

func TestSetTotalMinAndRejections(t *testing.T) {
	a, err := New(3, []int64{100, 300, 600}, []int64{8, 8, 1}, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustAlloc := func(preferred int, pages int64, gfp GFP, wantZone int) {
		t.Helper()
		zone, allocErr := a.Alloc(preferred, pages, gfp)
		if allocErr != nil || zone != wantZone {
			t.Fatalf("Alloc(%d, %d) = (%d, %v), want zone %d", preferred, pages, zone, allocErr, wantZone)
		}
	}
	mustAlloc(2, 510, NORMAL, 2)
	mustAlloc(2, 10, NORMAL, 2)
	mustAlloc(2, 10, NORMAL, 1)
	mustAlloc(1, 180, NORMAL, 1)
	mustAlloc(2, 10, NORMAL, 2)
	mustAlloc(2, 10, ATOMIC, 2)
	for range 3 {
		fresh, freshErr := New(3, []int64{100, 300, 600}, []int64{8, 8, 1}, 100)
		if freshErr != nil {
			t.Fatal(freshErr)
		}
		if _, allocErr := fresh.Alloc(2, 510, NORMAL); allocErr != nil {
			t.Fatal(allocErr)
		}
		if _, allocErr := fresh.Alloc(2, 10, NORMAL); allocErr != nil {
			t.Fatal(allocErr)
		}
		for count := 1; count <= 3; count++ {
			zone, allocErr := fresh.Alloc(2, 10, NORMAL)
			if allocErr != nil || zone != 1 {
				t.Fatalf("fresh fallback %d = (%d, %v), want zone 1", count, zone, allocErr)
			}
		}
		a = fresh
	}
	if boost, _ := a.Boost(2); boost != 45 {
		t.Fatalf("boost = %d, want 45", boost)
	}
	if err := a.SetTotalMin(0); err != nil {
		t.Fatal(err)
	}
	if boost, _ := a.Boost(2); boost != 0 {
		t.Fatalf("clamped boost = %d, want 0", boost)
	}

	b, err := New(1, []int64{10}, []int64{8}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, allocErr := b.Alloc(0, 0, NORMAL); !errors.Is(allocErr, ErrInvalidArgument) {
		t.Fatalf("Alloc error = %v", allocErr)
	}
	if freeErr := b.Free(0, 1); !errors.Is(freeErr, ErrOverfree) {
		t.Fatalf("Free error = %v, want ErrOverfree", freeErr)
	}
	if setErr := b.SetTotalMin(11); !errors.Is(setErr, ErrInvalidArgument) {
		t.Fatalf("SetTotalMin error = %v", setErr)
	}
	if got, _ := b.FreePages(0); got != 10 {
		t.Fatalf("free = %d, want unchanged 10", got)
	}
}

func TestInvalidConstruction(t *testing.T) {
	validManaged := []int64{100, 300, 600}
	validRatios := []int64{8, 8, 1}
	tests := []struct {
		name     string
		zones    int
		managed  []int64
		ratios   []int64
		totalMin int64
	}{
		{"zero zones", 0, validManaged, validRatios, 100},
		{"too many zones", 5, make([]int64, 5), make([]int64, 5), 0},
		{"managed length mismatch", 3, []int64{100, 300}, validRatios, 100},
		{"ratios length mismatch", 3, validManaged, []int64{8, 8}, 100},
		{"zero managed", 3, []int64{100, 0, 600}, validRatios, 100},
		{"managed too large", 3, []int64{100, 1_000_000_001, 600}, validRatios, 100},
		{"zero ratio", 3, validManaged, []int64{8, 0, 1}, 100},
		{"ratio too large", 3, validManaged, []int64{8, 8, 1001}, 100},
		{"negative total", 3, validManaged, validRatios, -1},
		{"total too large", 3, validManaged, validRatios, 1001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.zones, tt.managed, tt.ratios, tt.totalMin); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("New() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestDocumentedWalkthrough(t *testing.T) {
	a, err := New(3, []int64{100, 300, 600}, []int64{8, 8, 1}, 100)
	if err != nil {
		t.Fatal(err)
	}
	must := func(preferred int, pages int64, gfp GFP, wantZone int) {
		t.Helper()
		zone, allocErr := a.Alloc(preferred, pages, gfp)
		if allocErr != nil || zone != wantZone {
			t.Fatalf("Alloc(%d,%d,%v)=(%d,%v), want %d", preferred, pages, gfp, zone, allocErr, wantZone)
		}
	}
	must(2, 510, NORMAL, 2)
	must(2, 10, NORMAL, 2)
	must(2, 10, NORMAL, 1)
	must(1, 180, NORMAL, 1)
	must(2, 10, NORMAL, 2)
	if _, allocErr := a.Alloc(2, 10, NORMAL); !errors.Is(allocErr, ErrWouldReclaim) {
		t.Fatalf("Alloc() error = %v, want ErrWouldReclaim", allocErr)
	}
	must(2, 10, ATOMIC, 2)
	if err := a.Free(2, 40); err != nil {
		t.Fatal(err)
	}
	if awake, _ := a.Kswapd(2); !awake {
		t.Fatal("kswapd[2] should remain awake at equality")
	}
	if err := a.Free(2, 13); err != nil {
		t.Fatal(err)
	}
	if awake, _ := a.Kswapd(2); awake {
		t.Fatal("kswapd[2] should sleep above high + boost")
	}
	must(2, 113, EMERGENCY, 2)

	freePages := []int64{100, 110, 0}
	for zone, want := range freePages {
		if got, _ := a.FreePages(zone); got != want {
			t.Fatalf("free[%d] = %d, want %d", zone, got, want)
		}
	}
	stats := a.Stats()
	wantAllocations := []int64{0, 2, 5}
	for zone, want := range wantAllocations {
		if stats.Allocations[zone] != want {
			t.Fatalf("allocations[%d] = %d, want %d", zone, stats.Allocations[zone], want)
		}
	}
	if stats.Fallbacks != 1 || stats.Wakes != 1 {
		t.Fatalf("stats = %+v, want fallbacks=1 wakes=1", stats)
	}
}

func TestAtomicFallbackBoostAndOwnZoneSleep(t *testing.T) {
	a, err := New(2, []int64{100, 100}, []int64{1000, 1000}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, allocErr := a.Alloc(1, 94, EMERGENCY); allocErr != nil {
		t.Fatal(allocErr)
	}
	if _, allocErr := a.Alloc(0, 88, EMERGENCY); allocErr != nil {
		t.Fatal(allocErr)
	}
	zone, allocErr := a.Alloc(1, 1, ATOMIC)
	if allocErr != nil || zone != 0 {
		t.Fatalf("ATOMIC second-pass fallback = (%d, %v), want zone 0", zone, allocErr)
	}
	if boost, _ := a.Boost(1); boost != 3 {
		t.Fatalf("ATOMIC fallback boost = %d, want 3", boost)
	}
	if awake, _ := a.Kswapd(0); awake {
		t.Fatal("shortage in preferred zone must not set other kswapd")
	}
	if err := a.SetTotalMin(100); err != nil {
		t.Fatal(err)
	}
	if awake, _ := a.Kswapd(1); !awake {
		t.Fatal("raising marks must not clear kswapd below its own high + boost")
	}
	if awake, _ := a.Kswapd(0); awake {
		t.Fatal("raising marks must not set an unrelated kswapd")
	}
	if err := a.SetTotalMin(0); err != nil {
		t.Fatal(err)
	}
	if boost, _ := a.Boost(1); boost != 0 {
		t.Fatalf("boost after clamp-and-sleep = %d, want 0", boost)
	}
	if awake, _ := a.Kswapd(1); awake {
		t.Fatal("zone 1 should sleep after clamp makes threshold zero")
	}
}

func TestBoostPersistsWhileKswapdFalse(t *testing.T) {
	twoZones, err := New(2, []int64{100, 100}, []int64{1000, 1000}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, allocErr := twoZones.Alloc(1, 88, EMERGENCY); allocErr != nil {
		t.Fatal(allocErr)
	}
	zone, allocErr := twoZones.Alloc(1, 1, NORMAL)
	if allocErr != nil || zone != 0 {
		t.Fatalf("fallback = (%d, %v), want zone 0", zone, allocErr)
	}
	if err := twoZones.Free(1, 4); err != nil {
		t.Fatal(err)
	}
	if _, allocErr := twoZones.Alloc(0, 86, EMERGENCY); allocErr != nil {
		t.Fatal(allocErr)
	}
	if awake, _ := twoZones.Kswapd(1); awake {
		t.Fatal("successful fallback must leave kswapd false")
	}
	if boost, _ := twoZones.Boost(1); boost != 3 {
		t.Fatalf("boost with kswapd false = %d, want 3", boost)
	}
	if _, allocErr := twoZones.Alloc(1, 6, NORMAL); !errors.Is(allocErr, ErrWouldReclaim) {
		t.Fatalf("boosted first-pass request error = %v, want ErrWouldReclaim", allocErr)
	}
}
