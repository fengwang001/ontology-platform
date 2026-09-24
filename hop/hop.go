// Package hop computes hopping-window membership and close decisions.
// It has no dependencies on other packages in this module.
package hop

// Params describes a hopping window grid.
type Params struct {
	Size  int64 // window length
	Slide int64 // hop length
}

// Valid reports whether p is a legal grid: size>0, slide>0, size%slide==0.
func (p Params) Valid() bool {
	return p.Size > 0 && p.Slide > 0 && p.Size%p.Slide == 0
}

// HopCount returns size/slide, the exact number of windows any event
// belongs to. Caller must pass a valid Params.
func (p Params) HopCount() int64 {
	return p.Size / p.Slide
}

// Window is a half-open interval [Start, End) with grid index K.
type Window struct {
	K     int64
	Start int64
	End   int64
}

// floorDiv returns math.Floor(a/b) for b>0, unlike Go's truncated a/b.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// Ks returns the grid indices of the size/slide windows containing ts.
// A window is [k*slide, k*slide+size); negative timestamps are assigned
// with mathematical floor semantics (e.g. -5/4 floors to -2).
func (p Params) Ks(ts int64) []int64 {
	q := floorDiv(ts, p.Slide)
	n := p.HopCount()
	ks := make([]int64, n)
	for i := int64(0); i < n; i++ {
		ks[i] = q - (n - 1 - i) // oldest first
	}
	return ks
}

// At returns the window with grid index k.
func (p Params) At(k int64) Window {
	return Window{K: k, Start: k * p.Slide, End: k*p.Slide + p.Size}
}

// OpenKs filters ks to the windows still open at clock time clock,
// i.e. those with end > clock. ks must be ascending; the result is a
// fresh slice. An empty result means every containing window is closed.
func (p Params) OpenKs(ks []int64, clock int64) []int64 {
	var out []int64
	for _, k := range ks {
		if w := p.At(k); w.End > clock {
			out = append(out, k)
		}
	}
	return out
}
