package ontology

import (
	"math/rand"
	"testing"
)

// mustStats 读取全部统计量，任一报错则失败。
func mustStats(t *testing.T, a *Accumulator) (mean, pv, sv float64) {
	t.Helper()
	var err error
	if mean, err = a.Mean(); err != nil {
		t.Fatalf("Mean 返回错误: %v", err)
	}
	if pv, err = a.Variance(); err != nil {
		t.Fatalf("Variance 返回错误: %v", err)
	}
	if sv, err = a.SampleVariance(); err != nil {
		t.Fatalf("SampleVariance 返回错误: %v", err)
	}
	return mean, pv, sv
}

func fill(t *testing.T, xs []float64) *Accumulator {
	t.Helper()
	a := New()
	for _, x := range xs {
		if err := a.Add(x); err != nil {
			t.Fatalf("Add(%v) 返回错误: %v", x, err)
		}
	}
	return a
}

// Merge(a, b) 与 Merge(b, a) 的结果必须逐位相同。
func TestMergeCommutativeBitwise(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	a := fill(t, []float64{1, 2, 3, 4, 5})
	var xs []float64
	for i := 0; i < 7; i++ {
		xs = append(xs, rng.NormFloat64()*1e6-3e5)
	}
	b := fill(t, xs)

	ab := Merge(a, b)
	ba := Merge(b, a)
	if ab.Count() != ba.Count() || ab.Skipped() != ba.Skipped() {
		t.Fatalf("合并计数不一致: (%d,%d) vs (%d,%d)",
			ab.Count(), ab.Skipped(), ba.Count(), ba.Skipped())
	}
	m1, p1, s1 := mustStats(t, ab)
	m2, p2, s2 := mustStats(t, ba)
	if m1 != m2 || p1 != p2 || s1 != s2 {
		t.Fatalf("Merge 交换后结果不逐位相同: (%v,%v,%v) vs (%v,%v,%v)",
			m1, p1, s1, m2, p2, s2)
	}
}

// 合并空累加器是恒等操作：结果与源逐位一致；两个空的合并仍为空。
func TestMergeEmptyIdentity(t *testing.T) {
	a := fill(t, []float64{1e9, 1e9 + 1, 1e9 + 2})
	empty := New()

	for name, merged := range map[string]*Accumulator{
		"Merge(a, empty)": Merge(a, empty),
		"Merge(empty, a)": Merge(empty, a),
	} {
		if merged.Count() != a.Count() {
			t.Fatalf("%s 计数 = %d, want %d", name, merged.Count(), a.Count())
		}
		m, p, s := mustStats(t, merged)
		am, ap, as := mustStats(t, a)
		if m != am || p != ap || s != as {
			t.Fatalf("%s 不逐位等于 a: (%v,%v,%v) vs (%v,%v,%v)",
				name, m, p, s, am, ap, as)
		}
	}

	ee := Merge(New(), New())
	if ee.Count() != 0 || ee.Skipped() != 0 {
		t.Fatalf("两个空累加器合并后应为空, got count=%d skipped=%d",
			ee.Count(), ee.Skipped())
	}
	if _, err := ee.Mean(); err == nil {
		t.Fatal("空合并结果读取均值应报错")
	}
}

// 一万个样本随机切成若干段，各自累加后两两合并，
// 结果与一次性喂入的相对误差在 1e-12 以内。
// 样本取常规量级：1e9 级偏移下任何在线算法的自身舍入误差
// 都已超过 1e-12，该情形由真值对照的稳定性测试覆盖。
func TestMergeRandomPartitions(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const total = 10000
	samples := make([]float64, total)
	for i := range samples {
		samples[i] = 1000 + rng.NormFloat64()*100
	}
	oneShot := fill(t, samples)

	for trial := 0; trial < 20; trial++ {
		var parts []*Accumulator
		start := 0
		for start < total {
			size := 1 + rng.Intn(total/10)
			if start+size > total {
				size = total - start
			}
			parts = append(parts, fill(t, samples[start:start+size]))
			start += size
		}
		merged := parts[0]
		for _, p := range parts[1:] {
			merged = Merge(merged, p)
		}
		if merged.Count() != total {
			t.Fatalf("trial %d: 合并计数 = %d, want %d", trial, merged.Count(), total)
		}
		m, p, s := mustStats(t, merged)
		om, op, os := mustStats(t, oneShot)
		bad := relErr(m, om) > 1e-12 || relErr(p, op) > 1e-12 || relErr(s, os) > 1e-12
		if bad {
			t.Fatalf("trial %d: 分段合并与一次性喂入不一致: "+
				"mean %v vs %v, pv %v vs %v, sv %v vs %v",
				trial, m, om, p, op, s, os)
		}
	}
}

// Merge 不得修改两个源累加器：合并前后读数逐位相同。
func TestMergeDoesNotModifySources(t *testing.T) {
	a := fill(t, []float64{1, 2, 3})
	b := fill(t, []float64{10, 20, 30, 40})
	am, ap, as := mustStats(t, a)
	bm, bp, bs := mustStats(t, b)
	ac, bc := a.Count(), b.Count()

	_ = Merge(a, b)
	_ = Merge(b, a)

	if a.Count() != ac || b.Count() != bc {
		t.Fatal("Merge 修改了源的计数")
	}
	if m, p, s := mustStats(t, a); m != am || p != ap || s != as {
		t.Fatal("Merge 修改了源 a 的统计量")
	}
	if m, p, s := mustStats(t, b); m != bm || p != bp || s != bs {
		t.Fatal("Merge 修改了源 b 的统计量")
	}
}
