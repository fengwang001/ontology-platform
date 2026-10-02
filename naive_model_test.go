package ontology

import (
	"errors"
	"math/rand"
	"testing"
)

type naiveAllocator struct {
	zones    int
	managed  []int64
	ratios   []int64
	totalM   int64
	totalMin int64
	freeList []int64
	min      []int64
	low      []int64
	high     []int64
	amin     []int64
	step     []int64
	cap      []int64
	boost    []int64
	reserve  [][]int64
	kswapd   []bool
	allocs   []int64
	fallback int64
	wakes    int64
	checks   int64
}

func newNaive(managed, ratios []int64, totalMin int64) *naiveAllocator {
	z := len(managed)
	m := &naiveAllocator{
		zones:    z,
		managed:  append([]int64(nil), managed...),
		ratios:   append([]int64(nil), ratios...),
		totalM:   sumInt64(managed),
		totalMin: totalMin,
		freeList: append([]int64(nil), managed...),
		boost:    make([]int64, z),
		min:      make([]int64, z),
		low:      make([]int64, z),
		high:     make([]int64, z),
		amin:     make([]int64, z),
		step:     make([]int64, z),
		cap:      make([]int64, z),
		reserve:  make([][]int64, z),
		kswapd:   make([]bool, z),
		allocs:   make([]int64, z),
	}
	m.recalc(totalMin)
	for zone := 0; zone < z; zone++ {
		m.reserve[zone] = make([]int64, z)
		for preferred := zone + 1; preferred < z; preferred++ {
			m.reserve[zone][preferred] = sumInt64(managed[zone+1:preferred+1]) / ratios[zone]
		}
	}
	return m
}

func (m *naiveAllocator) recalc(totalMin int64) {
	if m.min == nil {
		m.min = make([]int64, m.zones)
		m.low = make([]int64, m.zones)
		m.high = make([]int64, m.zones)
		m.amin = make([]int64, m.zones)
		m.step = make([]int64, m.zones)
		m.cap = make([]int64, m.zones)
	}
	for z := 0; z < m.zones; z++ {
		minimum := totalMin * m.managed[z] / m.totalM
		m.min[z] = minimum
		m.low[z] = minimum + minimum/4
		m.high[z] = minimum + minimum/2
		m.amin[z] = minimum - minimum/2
		m.step[z] = maxInt64(1, m.high[z]/4)
		m.cap[z] = m.high[z] / 2
	}
}

func (m *naiveAllocator) ok(zone int, pages int64, watermark int64, preferred int) bool {
	return m.freeList[zone] > pages+watermark+m.reserve[zone][preferred]
}

func (m *naiveAllocator) alloc(preferred int, pages int64, gfp GFP) (int, error) {
	m.checks = 0
	if gfp == EMERGENCY {
		for z := preferred; z >= 0; z-- {
			if m.freeList[z] >= pages {
				m.freeList[z] -= pages
				m.allocs[z]++
				if z != preferred {
					m.fallback++
				}
				return z, nil
			}
		}
		return 0, ErrNoMemory
	}

	for z := preferred; z >= 0; z-- {
		m.checks++
		if m.ok(z, pages, m.low[z]+m.boost[z], preferred) {
			m.freeList[z] -= pages
			m.allocs[z]++
			if z != preferred {
				m.fallback++
				m.boost[preferred] = minInt64(m.cap[preferred], m.boost[preferred]+m.step[preferred])
			}
			return z, nil
		}
	}

	if !m.kswapd[preferred] {
		m.kswapd[preferred] = true
		m.wakes++
	}

	watermarks := m.min
	if gfp == ATOMIC {
		watermarks = m.amin
	}
	for z := preferred; z >= 0; z-- {
		m.checks++
		if m.ok(z, pages, watermarks[z], preferred) {
			m.freeList[z] -= pages
			m.allocs[z]++
			if z != preferred {
				m.fallback++
				m.boost[preferred] = minInt64(m.cap[preferred], m.boost[preferred]+m.step[preferred])
			}
			return z, nil
		}
	}

	if gfp == NORMAL {
		return 0, ErrWouldReclaim
	}
	return 0, ErrNoMemory
}

func (m *naiveAllocator) release(zone int, pages int64) error {
	if m.freeList[zone] > m.managed[zone]-pages {
		return ErrOverfree
	}
	m.freeList[zone] += pages
	for z := 0; z < m.zones; z++ {
		if m.kswapd[z] && m.freeList[z] > m.high[z]+m.boost[z] {
			m.kswapd[z] = false
			m.boost[z] = 0
		}
	}
	return nil
}

func (m *naiveAllocator) setTotalMin(totalMin int64) {
	m.totalMin = totalMin
	m.recalc(totalMin)
	for z := range m.boost {
		m.boost[z] = minInt64(m.boost[z], m.cap[z])
	}
	for z := 0; z < m.zones; z++ {
		if m.kswapd[z] && m.freeList[z] > m.high[z]+m.boost[z] {
			m.kswapd[z] = false
			m.boost[z] = 0
		}
	}
}

func sumInt64(values []int64) int64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return total
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))

	for sequence := 0; sequence < 2000; sequence++ {
		z := 1 + rng.Intn(4)
		managed := make([]int64, z)
		ratios := make([]int64, z)
		for i := range managed {
			managed[i] = int64(1 + rng.Intn(120))
			ratios[i] = int64(1 + rng.Intn(20))
		}
		totalMin := rng.Int63n(sumInt64(managed) + 1)
		actual, err := NewPageAllocator(z, managed, ratios, totalMin)
		if err != nil {
			t.Fatalf("sequence %d constructor error = %v", sequence, err)
		}
		naive := newNaive(managed, ratios, totalMin)

		for step := 0; step < 90; step++ {
			switch rng.Intn(5) {
			case 0, 1, 2:
				preferred := rng.Intn(z)
				pages := int64(1 + rng.Intn(120))
				gfp := GFP(rng.Intn(3))
				gotZone, gotErr := actual.Alloc(preferred, pages, gfp)
				wantZone, wantErr := naive.alloc(preferred, pages, gfp)
				t.Logf("seq=%d step=%d input=Alloc(p=%d,n=%d,gfp=%d) output=(z=%d,err=%v); basis: chain p..0, pass1 low+boost+reserve, pass2 %s",
					sequence, step, preferred, pages, gfp, gotZone, gotErr, gfpName(gfp))
				if gotZone != wantZone || !errors.Is(gotErr, wantErr) {
					t.Fatalf("Alloc mismatch: got=(%d,%v) want=(%d,%v)", gotZone, gotErr, wantZone, wantErr)
				}
			case 3:
				zone := rng.Intn(z)
				pages := int64(1 + rng.Intn(120))
				gotErr := actual.Free(zone, pages)
				wantErr := naive.release(zone, pages)
				t.Logf("seq=%d step=%d input=Free(z=%d,n=%d) output=%v; basis: add F then sleep if F > high+boost",
					sequence, step, zone, pages, gotErr)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("Free mismatch: got=%v want=%v", gotErr, wantErr)
				}
			default:
				value := rng.Int63n(sumInt64(managed) + 1)
				gotErr := actual.SetTotalMin(value)
				naive.setTotalMin(value)
				t.Logf("seq=%d step=%d input=SetTotalMin(v=%d) output=%v; basis: derive marks, clamp boost, only clear kswapd",
					sequence, step, value, gotErr)
				if gotErr != nil {
					t.Fatalf("SetTotalMin mismatch: got=%v want=nil", gotErr)
				}
			}
			compareModel(t, actual, naive, sequence, step)
		}
	}
}

func gfpName(gfp GFP) string {
	switch gfp {
	case NORMAL:
		return "min"
	case ATOMIC:
		return "amin"
	default:
		return "ignored"
	}
}

func compareModel(t *testing.T, actual *PageAllocator, naive *naiveAllocator, sequence, step int) {
	t.Helper()
	if actual.totalM != naive.totalM || actual.totalMin != naive.totalMin {
		t.Fatalf("sequence %d step %d total mismatch", sequence, step)
	}
	if !equalInt64(actual.free, naive.freeList) ||
		!equalInt64(actual.min, naive.min) ||
		!equalInt64(actual.low, naive.low) ||
		!equalInt64(actual.high, naive.high) ||
		!equalInt64(actual.atomic, naive.amin) ||
		!equalInt64(actual.step, naive.step) ||
		!equalInt64(actual.cap, naive.cap) ||
		!equalInt64(actual.boost, naive.boost) ||
		!equalInt64(actual.allocs, naive.allocs) ||
		actual.fallback != naive.fallback ||
		actual.wakes != naive.wakes ||
		actual.checks != naive.checks ||
		!equalBool(actual.kswapd, naive.kswapd) {
		t.Fatalf("sequence %d step %d state mismatch:\nactual=%+v\nnaive=%+v", sequence, step, actual, naive)
	}
}

func equalInt64(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalBool(left, right []bool) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
