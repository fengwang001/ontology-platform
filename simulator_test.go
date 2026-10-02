package pagealloc

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
)

type naiveModel struct {
	zones      int
	managed    []int64
	ratios     []int64
	free       []int64
	min        []int64
	low        []int64
	high       []int64
	atomic     []int64
	step       []int64
	cap        []int64
	boost      []int64
	reserve    [][]int64
	kswapd     []bool
	allocs     []int64
	fallbacks  int64
	wakes      int64
	checks     int64
	totalMin   int64
	totalPages int64
}

type modelResult struct {
	zone int
	err  error
}

func newNaiveModel(zones int, managed []int64, ratios []int64, totalMin int64) *naiveModel {
	m := &naiveModel{
		zones:    zones,
		managed:  append([]int64(nil), managed...),
		ratios:   append([]int64(nil), ratios...),
		free:     append([]int64(nil), managed...),
		boost:    make([]int64, zones),
		reserve:  make([][]int64, zones),
		kswapd:   make([]bool, zones),
		allocs:   make([]int64, zones),
		totalMin: totalMin,
	}
	for _, pages := range managed {
		m.totalPages += pages
	}
	for zone := range m.reserve {
		m.reserve[zone] = make([]int64, zones)
	}
	m.recalc()
	for zone := 0; zone < zones; zone++ {
		for preferred := zone + 1; preferred < zones; preferred++ {
			var included int64
			for source := zone + 1; source <= preferred; source++ {
				included += m.managed[source]
			}
			m.reserve[zone][preferred] = included / m.ratios[zone]
		}
	}
	return m
}

func (m *naiveModel) recalc() {
	m.min = make([]int64, m.zones)
	m.low = make([]int64, m.zones)
	m.high = make([]int64, m.zones)
	m.atomic = make([]int64, m.zones)
	m.step = make([]int64, m.zones)
	m.cap = make([]int64, m.zones)
	for zone := range m.managed {
		minimum := m.totalMin * m.managed[zone] / m.totalPages
		m.min[zone] = minimum
		m.low[zone] = minimum + minimum/4
		m.high[zone] = minimum + minimum/2
		m.atomic[zone] = minimum - minimum/2
		m.step[zone] = max64(1, m.high[zone]/4)
		m.cap[zone] = m.high[zone] / 2
	}
}

func (m *naiveModel) alloc(preferred int, pages int64, gfp GFP) modelResult {
	if preferred < 0 || preferred >= m.zones || pages < 1 || pages > 1_000_000_000 || (gfp != NORMAL && gfp != ATOMIC && gfp != EMERGENCY) {
		return modelResult{err: ErrInvalidArgument}
	}
	if gfp == EMERGENCY {
		for zone := preferred; zone >= 0; zone-- {
			m.checks++
			if m.free[zone] >= pages {
				m.complete(zone, preferred, pages, gfp)
				return modelResult{zone: zone}
			}
		}
		return modelResult{err: ErrNoMemory}
	}
	for zone := preferred; zone >= 0; zone-- {
		m.checks++
		if m.free[zone]-pages > m.low[zone]+m.boost[zone]+m.reserve[zone][preferred] {
			m.complete(zone, preferred, pages, gfp)
			return modelResult{zone: zone}
		}
	}
	if !m.kswapd[preferred] {
		m.kswapd[preferred] = true
		m.wakes++
	}
	mark := m.min
	if gfp == ATOMIC {
		mark = m.atomic
	}
	for zone := preferred; zone >= 0; zone-- {
		m.checks++
		if m.free[zone]-pages > mark[zone]+m.reserve[zone][preferred] {
			m.complete(zone, preferred, pages, gfp)
			return modelResult{zone: zone}
		}
	}
	if gfp == ATOMIC {
		return modelResult{err: ErrNoMemory}
	}
	return modelResult{err: ErrWouldReclaim}
}

func (m *naiveModel) complete(zone, preferred int, pages int64, gfp GFP) {
	m.free[zone] -= pages
	m.allocs[zone]++
	if zone != preferred {
		m.fallbacks++
	}
	if zone != preferred && gfp != EMERGENCY {
		m.boost[preferred] = min(m.cap[preferred], m.boost[preferred]+m.step[preferred])
	}
}

func (m *naiveModel) freePages(zone int, pages int64) error {
	if zone < 0 || zone >= m.zones || pages < 1 || pages > 1_000_000_000 {
		return ErrInvalidArgument
	}
	if m.free[zone]+pages > m.managed[zone] {
		return ErrOverfree
	}
	m.free[zone] += pages
	for preferred := range m.kswapd {
		if m.kswapd[preferred] && m.free[preferred] > m.high[preferred]+m.boost[preferred] {
			m.kswapd[preferred] = false
			m.boost[preferred] = 0
		}
	}
	return nil
}

func (m *naiveModel) setTotalMin(totalMin int64) error {
	if totalMin < 0 || totalMin > m.totalPages {
		return ErrInvalidArgument
	}
	m.totalMin = totalMin
	m.recalc()
	for zone := range m.boost {
		m.boost[zone] = min(m.boost[zone], m.cap[zone])
	}
	for zone := range m.kswapd {
		if m.kswapd[zone] && m.free[zone] > m.high[zone]+m.boost[zone] {
			m.kswapd[zone] = false
			m.boost[zone] = 0
		}
	}
	return nil
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		rng := rand.New(rand.NewPCG(uint64(sequence+1), uint64(9001-sequence)))
		zones := 1 + rng.IntN(4)
		managed := make([]int64, zones)
		ratios := make([]int64, zones)
		var totalPages int64
		for zone := range managed {
			if rng.IntN(5) == 0 {
				managed[zone] = 1 + rng.Int64N(20)
			} else {
				managed[zone] = 1 + rng.Int64N(500)
			}
			ratios[zone] = 1 + int64(rng.IntN(10))
			totalPages += managed[zone]
		}
		initialTotalMin := rng.Int64N(totalPages + 1)
		actual, err := New(zones, managed, ratios, initialTotalMin)
		if err != nil {
			t.Fatalf("sequence %d New() error = %v", sequence, err)
		}
		model := newNaiveModel(zones, managed, ratios, initialTotalMin)

		for step := 0; step < 12; step++ {
			beforeChecks := actual.Checks()
			op := rng.IntN(10)
			switch {
			case op < 6:
				preferred := rng.IntN(zones)
				pages := 1 + rng.Int64N(80)
				gfp := []GFP{NORMAL, NORMAL, NORMAL, ATOMIC, EMERGENCY}[rng.IntN(5)]
				gotZone, gotErr := actual.Alloc(preferred, pages, gfp)
				want := model.alloc(preferred, pages, gfp)
				t.Logf("seq=%d step=%d Alloc(p=%d,n=%d,gfp=%d) -> zone=%d err=%v; fallback scans preferred..0 with low/min/atomic marks", sequence, step, preferred, pages, gfp, gotZone, gotErr)
				if gotZone != want.zone || !errors.Is(gotErr, want.err) {
					t.Fatalf("Alloc mismatch: got (%d,%v), want (%d,%v)", gotZone, gotErr, want.zone, want.err)
				}
			case op < 8:
				zone := rng.IntN(zones)
				freeNow, _ := actual.FreePages(zone)
				pages := 1 + rng.Int64N(max64(1, managed[zone]-freeNow+2))
				gotErr := actual.Free(zone, pages)
				wantErr := model.freePages(zone, pages)
				t.Logf("seq=%d step=%d Free(z=%d,n=%d) -> err=%v; free=%d managed=%d, sleeps kswapd only above high+boost", sequence, step, zone, pages, gotErr, freeNow, managed[zone])
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("Free mismatch: got %v, want %v", gotErr, wantErr)
				}
			case op == 8:
				totalMin := rng.Int64N(totalPages + 1)
				gotErr := actual.SetTotalMin(totalMin)
				wantErr := model.setTotalMin(totalMin)
				t.Logf("seq=%d step=%d SetTotalMin(v=%d) -> err=%v; recomputes integer floor marks then clamps boost", sequence, step, totalMin, gotErr)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("SetTotalMin mismatch: got %v, want %v", gotErr, wantErr)
				}
			default:
				zone := rng.IntN(zones)
				freePages, _ := actual.FreePages(zone)
				boost, _ := actual.Boost(zone)
				kswapd, _ := actual.Kswapd(zone)
				t.Logf("seq=%d step=%d Query(z=%d): free=%d boost=%d kswapd=%t", sequence, step, zone, freePages, boost, kswapd)
			}
			compareModel(t, actual, model, sequence, step)
			usedChecks := actual.Checks() - beforeChecks
			if usedChecks > int64(2*zones) {
				t.Fatalf("sequence %d step %d used %d checks, limit %d", sequence, step, usedChecks, 2*zones)
			}
		}
	}
}

func compareModel(t *testing.T, actual *Allocator, model *naiveModel, sequence, step int) {
	t.Helper()
	for zone := range model.managed {
		gotFree, _ := actual.FreePages(zone)
		if gotFree != model.free[zone] {
			t.Fatalf("seq=%d step=%d free[%d]=%d, want %d", sequence, step, zone, gotFree, model.free[zone])
		}
		gotBoost, _ := actual.Boost(zone)
		if gotBoost != model.boost[zone] {
			t.Fatalf("seq=%d step=%d boost[%d]=%d, want %d", sequence, step, zone, gotBoost, model.boost[zone])
		}
		gotKswapd, _ := actual.Kswapd(zone)
		if gotKswapd != model.kswapd[zone] {
			t.Fatalf("seq=%d step=%d kswapd[%d]=%t, want %t", sequence, step, zone, gotKswapd, model.kswapd[zone])
		}
		gotMarks, _ := actual.Marks(zone)
		wantMarks := Marks{Min: model.min[zone], Low: model.low[zone], High: model.high[zone], Atomic: model.atomic[zone]}
		if gotMarks != wantMarks {
			t.Fatalf("seq=%d step=%d marks[%d]=%+v, want %+v", sequence, step, zone, gotMarks, wantMarks)
		}
	}
	gotStats := actual.Stats()
	for zone := range model.allocs {
		if gotStats.Allocations[zone] != model.allocs[zone] {
			t.Fatalf("seq=%d step=%d allocs[%d]=%d, want %d", sequence, step, zone, gotStats.Allocations[zone], model.allocs[zone])
		}
	}
	if gotStats.Fallbacks != model.fallbacks || gotStats.Wakes != model.wakes || actual.Checks() != model.checks {
		t.Fatalf("seq=%d step=%d stats=%+v checks=%d, want fallbacks=%d wakes=%d checks=%d", sequence, step, gotStats, actual.Checks(), model.fallbacks, model.wakes, model.checks)
	}
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func TestConcurrentOperations(t *testing.T) {
	a, err := New(4, []int64{200, 200, 200, 200}, []int64{4, 4, 4, 4}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, seed+100))
			for range 200 {
				zone := rng.IntN(4)
				pages := 1 + rng.Int64N(40)
				switch rng.IntN(6) {
				case 0:
					_, _ = a.Alloc(zone, pages, NORMAL)
				case 1:
					_, _ = a.Alloc(zone, pages, ATOMIC)
				case 2:
					_, _ = a.Alloc(zone, pages, EMERGENCY)
				case 3:
					freeNow, _ := a.FreePages(zone)
					if freeNow < 200 {
						_ = a.Free(zone, 1)
					}
				case 4:
					_ = a.SetTotalMin(rng.Int64N(801))
				default:
					_, _ = a.FreePages(zone)
					_, _ = a.Boost(zone)
					_, _ = a.Kswapd(zone)
				}
			}
		}(uint64(worker + 1))
	}
	wg.Wait()

	for zone := 0; zone < 4; zone++ {
		freePages, _ := a.FreePages(zone)
		if freePages < 0 || freePages > 200 {
			t.Fatalf("free[%d] = %d outside [0,200]", zone, freePages)
		}
		marks, _ := a.Marks(zone)
		boost, _ := a.Boost(zone)
		if !(marks.Atomic <= marks.Min && marks.Min <= marks.Low && marks.Low <= marks.High) {
			t.Fatalf("invalid mark order in zone %d: %+v", zone, marks)
		}
		if boost < 0 || boost > marks.High/2 {
			t.Fatalf("invalid boost[%d] = %d, cap=%d", zone, boost, marks.High/2)
		}
	}
}
