package microgrid

import "sort"

// forecast stores critical-load forecasts and island surplus per slot.
// Loads are held as a sorted slot list with a rebuilt prefix-sum array so
// that a single reserve-window sum costs O(log n) in the number of
// registered forecasts and never scans the forecast history. Updates are
// operator-driven and rebuild the prefix array; they have no stated bound.
type forecast struct {
	load    map[int]int
	surplus map[int]int
	keys    []int
	prefix  []int
}

func newForecast() *forecast {
	return &forecast{load: map[int]int{}, surplus: map[int]int{}}
}

// setLoad registers one slot's critical load forecast.
func (f *forecast) setLoad(slot, load int) {
	f.load[slot] = load
	f.rebuild()
}

// setLoads registers a consecutive range of forecasts.
func (f *forecast) setLoads(start int, loads []int) {
	for i, v := range loads {
		f.load[start+i] = v
	}
	f.rebuild()
}

// loadAt reports the forecast and whether it exists.
func (f *forecast) loadAt(slot int) (int, bool) {
	v, ok := f.load[slot]
	return v, ok
}

// surplusAt reports registered local generation surplus (default zero).
func (f *forecast) surplusAt(slot int) int {
	return f.surplus[slot]
}

// setSurplus registers local generation surplus for one future slot.
func (f *forecast) setSurplus(slot, surplus int) { f.surplus[slot] = surplus }

// rebuild sorts known slots and refreshes prefix sums.
func (f *forecast) rebuild() {
	f.keys = f.keys[:0]
	for k := range f.load {
		f.keys = append(f.keys, k)
	}
	sort.Ints(f.keys)
	f.prefix = make([]int, len(f.keys)+1)
	for i, k := range f.keys {
		f.prefix[i+1] = f.prefix[i] + f.load[k]
	}
}

// rangeSum returns the sum of forecasts with slot in [lo, hi] and the
// number of such slots. It is O(log n) via binary search on prefix sums.
func (f *forecast) rangeSum(lo, hi int) (sum, count int) {
	if lo > hi || len(f.keys) == 0 {
		return 0, 0
	}
	li := sort.SearchInts(f.keys, lo)
	ri := sort.SearchInts(f.keys, hi+1)
	return f.prefix[ri] - f.prefix[li], ri - li
}

// windowSum sums critical load over the next n slots after slot and reports
// whether every one of those n slots carries a forecast.
func (f *forecast) windowSum(slot, n int) (int, bool) {
	if n <= 0 {
		return 0, true
	}
	sum, count := f.rangeSum(slot+1, slot+n)
	return sum, count == n
}
