package topk_test

import (
	"slices"
	"strconv"
	"testing"

	"ontology/bound"
	"ontology/sketch"
	"ontology/topk"
)

// counter wraps a sketch and counts Estimate calls, to observe how many
// cell probes HeavyHitters triggers.
type counter struct {
	s     *sketch.Sketch
	calls int
}

func (c *counter) Add(k string, n uint64) error { return c.s.Add(k, n) }
func (c *counter) Estimate(k string) (uint64, error) {
	c.calls++
	return c.s.Estimate(k)
}
func (c *counter) Total() uint64 { return c.s.Total() }

func TestHeavyHittersAccessProportionalToCandidates(t *testing.T) {
	for _, w := range []int{1000, 100000} {
		c := &counter{s: mustSketch(t, w, 4)}
		d := mustDetector(t, c, 0.5)
		for i := 0; i < 10; i++ {
			d.Add("hot", 1) // becomes the only candidate
		}
		d.Add("cold", 1)
		c.calls = 0
		if hh := d.HeavyHitters(); !slices.Equal(hh, []string{"hot"}) {
			t.Fatalf("w=%d: HeavyHitters=%v", w, hh)
		}
		if c.calls != 1 { // one Estimate per candidate, never w*d
			t.Fatalf("w=%d: HeavyHitters triggered %d estimates, want 1", w, c.calls)
		}
	}
}

func TestNoMissAndDeterministicOrder(t *testing.T) {
	for _, phi := range []float64{0.01, 0.1, 0.5} {
		c := &counter{s: mustSketch(t, 128, 5)}
		d := mustDetector(t, c, phi)
		truth := map[string]uint64{}
		for i := 0; i < 1000; i++ {
			k := "k" + strconv.Itoa(i%50)
			if i%97 == 0 {
				k = "hot" + strconv.Itoa(i%3)
			}
			d.Add(k, 1)
			truth[k]++
		}
		th := uint64(phi*float64(c.Total())) + 1
		hh := d.HeavyHitters()
		if !slices.IsSorted(hh) {
			t.Fatalf("phi=%v: result not sorted", phi)
		}
		for k, n := range truth {
			if n >= th && !slices.Contains(hh, k) {
				t.Fatalf("phi=%v: true heavy hitter %q (%d) missed", phi, k, n)
			}
		}
	}
}

func TestBadPhi(t *testing.T) {
	for _, phi := range []float64{0, -0.1, 1, 1.5} {
		if _, err := topk.NewDetector(mustSketch(t, 8, 2), bound.Params{W: 8, D: 2}, phi); err == nil {
			t.Fatalf("phi=%v: want error", phi)
		}
	}
}

func mustSketch(t *testing.T, w, d int) *sketch.Sketch {
	t.Helper()
	s, err := sketch.New(w, d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustDetector(t *testing.T, c *counter, phi float64) *topk.Detector {
	t.Helper()
	w, d := c.s.Dims()
	det, err := topk.NewDetector(c, bound.Params{W: w, D: d}, phi)
	if err != nil {
		t.Fatal(err)
	}
	return det
}
