package ontology

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

func mustAcc(t *testing.T, xs []float64) *Accumulator {
	t.Helper()
	var a Accumulator
	for _, x := range xs {
		if err := a.Add(x); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	return &a
}

func bitsOf(t *testing.T, a *Accumulator) (int64, uint64, uint64) {
	t.Helper()
	s := lockSnapshot(a)
	return s.n, math.Float64bits(s.mean), math.Float64bits(s.m2)
}

// Merge(a, b) 与 Merge(b, a) 的结果必须逐位相同。
func TestMergeCommutativeBitwise(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 200; trial++ {
		var xs, ys []float64
		for i := 0; i < rng.IntN(50)+1; i++ {
			xs = append(xs, rng.NormFloat64()*1e6)
		}
		for i := 0; i < rng.IntN(50)+1; i++ {
			ys = append(ys, rng.NormFloat64()*1e-3)
		}
		a := mustAcc(t, xs)
		b := mustAcc(t, ys)
		ab := Merge(a, b)
		ba := Merge(b, a)
		n1, mean1, m21 := bitsOf(t, ab)
		n2, mean2, m22 := bitsOf(t, ba)
		if n1 != n2 || mean1 != mean2 || m21 != m22 {
			t.Fatalf("trial %d: Merge not bitwise commutative", trial)
		}
	}
}

// 一万个样本随机切段、各自累加后两两合并，与一次性喂入相对误差不超过 1e-12。
func TestMergeSegmentedVsOneShot(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	const total = 10000
	samples := make([]float64, total)
	for i := range samples {
		samples[i] = 1e6 + rng.NormFloat64()*1e3
	}

	oneShot := mustAcc(t, samples)

	var segs []*Accumulator
	for i := 0; i < total; {
		size := rng.IntN(997) + 1
		if i+size > total {
			size = total - i
		}
		segs = append(segs, mustAcc(t, samples[i:i+size]))
		i += size
	}
	if len(segs) < 10 {
		t.Fatalf("too few segments: %d", len(segs))
	}

	// 树状两两合并：误差随层数对数增长，比左折叠更稳。
	for len(segs) > 1 {
		var next []*Accumulator
		for i := 0; i < len(segs); i += 2 {
			if i+1 < len(segs) {
				next = append(next, Merge(segs[i], segs[i+1]))
			} else {
				next = append(next, segs[i])
			}
		}
		segs = next
	}
	merged := segs[0]

	if merged.Count() != total {
		t.Fatalf("Count = %d, want %d", merged.Count(), total)
	}
	mGot, _ := merged.Mean()
	mWant, _ := oneShot.Mean()
	if e := relErr(mGot, mWant); e > 1e-12 {
		t.Fatalf("mean rel err %v > 1e-12 (%v vs %v)", e, mGot, mWant)
	}
	vGot, _ := merged.SampleVariance()
	vWant, _ := oneShot.SampleVariance()
	if e := relErr(vGot, vWant); e > 1e-12 {
		t.Fatalf("sample variance rel err %v > 1e-12 (%v vs %v)", e, vGot, vWant)
	}
}

// 空累加器是合并的恒等元：合并前后逐位不变；两个空合并仍为空。
func TestMergeEmptyIdentity(t *testing.T) {
	a := mustAcc(t, []float64{1.5, -2.5, 3.5, 1e9})
	empty := &Accumulator{}

	for name, m := range map[string]*Accumulator{
		"Merge(a, empty)": Merge(a, empty),
		"Merge(empty, a)": Merge(empty, a),
	} {
		n, mean, m2 := bitsOf(t, m)
		wn, wmean, wm2 := bitsOf(t, a)
		if n != wn || mean != wmean || m2 != wm2 {
			t.Fatalf("%s not bitwise identical to a", name)
		}
	}

	ee := Merge(&Accumulator{}, &Accumulator{})
	if ee.Count() != 0 {
		t.Fatalf("Merge(empty, empty).Count = %d, want 0", ee.Count())
	}
	if _, err := ee.Mean(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Merge(empty, empty).Mean err = %v, want ErrNoSamples", err)
	}
}

// Merge 不得修改两个源累加器：合并前后读数逐位相同。
func TestMergeDoesNotMutateSources(t *testing.T) {
	a := mustAcc(t, []float64{1, 2, 3, 4, 5})
	b := mustAcc(t, []float64{10, 20, 30})
	an, amean, am2 := bitsOf(t, a)
	bn, bmean, bm2 := bitsOf(t, b)
	ask, bsk := a.Skipped(), b.Skipped()

	_ = Merge(a, b)
	_ = Merge(b, a)

	if n, mean, m2 := bitsOf(t, a); n != an || mean != amean || m2 != am2 {
		t.Fatal("Merge mutated source a")
	}
	if n, mean, m2 := bitsOf(t, b); n != bn || mean != bmean || m2 != bm2 {
		t.Fatal("Merge mutated source b")
	}
	if a.Skipped() != ask || b.Skipped() != bsk {
		t.Fatal("Merge mutated source skipped counters")
	}
}

// 任一方被正负 Inf 污染时，合并结果同样不可用；跳过计数取两者之和。
func TestMergePoisonedAndSkipped(t *testing.T) {
	var poisoned Accumulator
	_ = poisoned.Add(1)
	_ = poisoned.Add(math.Inf(1))
	_ = poisoned.Add(math.NaN())

	normal := mustAcc(t, []float64{2, 3})
	_ = normal.Add(math.NaN())
	_ = normal.Add(math.NaN())

	m := Merge(&poisoned, normal)
	if _, err := m.Mean(); !errors.Is(err, ErrStatsUnavailable) {
		t.Fatalf("merged Mean err = %v, want ErrStatsUnavailable", err)
	}
	if m.Count() != 4 {
		t.Fatalf("merged Count = %d, want 4", m.Count())
	}
	if m.Skipped() != 3 {
		t.Fatalf("merged Skipped = %d, want 3", m.Skipped())
	}
}
