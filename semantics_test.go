package quantile

import (
	"errors"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestSignedZeroMerged(t *testing.T) {
	s := NewSketch()
	if err := s.Add(math.Copysign(0, -1), 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(0, 3); err != nil {
		t.Fatal(err)
	}
	if s.UniqueCount() != 1 || s.TotalWeight() != 5 {
		t.Fatalf("unique=%d total=%d, want 1/5", s.UniqueCount(), s.TotalWeight())
	}
	for _, p := range []float64{0, 0.5, 1} {
		for _, q := range []func(float64) (float64, error){
			s.QuantileNearestRank, s.QuantileLinear,
		} {
			v, err := q(p)
			if err != nil {
				t.Fatal(err)
			}
			if bits(v) != bits(0.0) {
				t.Fatalf("p=%v returned %v, want canonical +0.0", p, v)
			}
		}
	}
}

func TestNaNSamplesRejectedAndCounted(t *testing.T) {
	s := NewSketch()
	for i := 0; i < 3; i++ {
		if err := s.Add(math.NaN()); !errors.Is(err, ErrNaNValue) {
			t.Fatalf("NaN add %d: %v", i, err)
		}
	}
	if err := s.AddWeighted(math.NaN(), 2); !errors.Is(err, ErrNaNValue) {
		t.Fatalf("weighted NaN: %v", err)
	}
	if s.SkippedNaN() != 4 {
		t.Fatalf("skipped = %d, want 4", s.SkippedNaN())
	}
	if s.UniqueCount() != 0 || s.TotalWeight() != 0 {
		t.Fatal("NaN must not be stored or weighted")
	}
	// Add a real value; NaN must never influence results.
	if err := s.Add(3.14); err != nil {
		t.Fatal(err)
	}
	v, err := s.QuantileLinear(0.5)
	if err != nil || math.IsNaN(v) || v != 3.14 {
		t.Fatalf("result polluted by NaN: %v %v", v, err)
	}
}

func TestOrderIndependenceAndIdempotentQueries(t *testing.T) {
	base := []entry{{1, 4}, {2, 1}, {3, 9}, {4, 2}, {5, 5}}
	rng := rand.New(rand.NewPCG(7, 9))
	var refs []pair
	first := weightedSketch(t, base)
	for i := 0; i < 300; i++ {
		p := float64(i) / 299
		nr, _ := first.QuantileNearestRank(p)
		lin, _ := first.QuantileLinear(p)
		refs = append(refs, pair{nr, lin})
	}
	for iter := 0; iter < 8; iter++ {
		shuffled := append([]entry(nil), base...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		s := weightedSketch(t, shuffled)
		for i := 0; i < len(refs); i++ {
			p := float64(i) / 299
			nr, err := s.QuantileNearestRank(p)
			if err != nil {
				t.Fatal(err)
			}
			lin, err := s.QuantileLinear(p)
			if err != nil {
				t.Fatal(err)
			}
			if bits(nr) != bits(refs[i].nr) || bits(lin) != bits(refs[i].lin) {
				t.Fatalf("iter %d p=%v order-dependent: %v/%v vs %v/%v",
					iter, p, nr, lin, refs[i].nr, refs[i].lin)
			}
		}
	}
	// Repeated queries never mutate state.
	before := s2state(first)
	for i := 0; i < 50; i++ {
		_, _ = first.QuantileLinear(0.37)
		_, _ = first.QuantileNearestRank(0.81)
	}
	if after := s2state(first); after != before {
		t.Fatalf("query mutated state: %s -> %s", before, after)
	}
}

type pair struct{ nr, lin float64 }

func s2state(s *Sketch) snapshotState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	for _, q := range s.points {
		b.WriteString(strconv.FormatFloat(q.value, 'g', -1, 64))
		b.WriteByte('=')
		b.WriteString(strconv.FormatUint(q.weight, 10))
		b.WriteByte(';')
	}
	b.WriteString("total=")
	b.WriteString(strconv.FormatUint(s.total, 10))
	return snapshotState(b.String())
}

type snapshotState string

func TestConcurrentQueries(t *testing.T) {
	entries := []entry{{-10, 100}, {0, 1}, {5, 33}, {99, 64}}
	s := weightedSketch(t, entries)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, seed+1))
			for i := 0; i < 2000; i++ {
				p := rng.Float64()
				a, err := s.QuantileNearestRank(p)
				if err != nil {
					t.Error(err)
					return
				}
				b, err := s.QuantileLinear(p)
				if err != nil {
					t.Error(err)
					return
				}
				// Deterministic consumption keeps the race detector busy on
				// the returned values and state reads.
				if math.IsNaN(a) {
					t.Error("nearest rank must never be NaN")
					return
				}
				_ = b
			}
		}(uint64(g + 1))
	}
	wg.Wait()
}
