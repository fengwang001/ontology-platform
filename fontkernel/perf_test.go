package fontkernel

import (
	"testing"
)

// TestCoverageLookupSublinear 用区间树内部的比较计数器 StabCount 证明：
// 判定一个码点落入哪些区间的开销不随区间总数 N 线性增长。
// 构造 2^k 个互不相交区间，对同一固定码点（仅命中一个区间）查询，
// 比较次数应随 N 倍增只增加常数级（O(log N) 次树层下降，每层一次二分）。
func TestCoverageLookupSublinear(t *testing.T) {
	build := func(n int) (*centroidTree, int) {
		rs := make([]faceRuneRange, n)
		for i := 0; i < n; i++ {
			base := rune(i * 4)
			rs[i] = faceRuneRange{lo: base, hi: base + 1, face: i}
		}
		return newCentroidTree(rs), n
	}
	var prevCount int64
	var prevN int
	for k := 4; k <= 14; k++ {
		n := 1 << k
		tree, _ := build(n)
		tree.StabCount = 0
		_ = tree.stab(0) // 只命中区间 0
		count := tree.StabCount
		if prevN > 0 {
			// 若为线性，倍增时比较次数也应近乎倍增；O(log N) 时增量应很小。
			growth := float64(count) / float64(prevCount)
			linearFloor := float64(n) / float64(prevN) * 0.6
			if growth > linearFloor {
				t.Errorf("N %d->%d comparisons %d->%d growth=%.2f looks linear",
					prevN, n, prevCount, count, growth)
			}
		}
		t.Logf("N=%5d stab comparisons=%d", n, count)
		prevN, prevCount = n, count
	}
}

// TestMatchingIndependentOfTotalFaces 证明单字符匹配不随“族内人脸总数”线性：
// 构造 N 张人脸，只有一张覆盖目标码点，匹配所需遍历的候选集合规模恒为 1。
func TestMatchingIndependentOfTotalFaces(t *testing.T) {
	for _, n := range []int{64, 256, 1024, 4096} {
		e, _ := testEngine()
		faces := make([]FaceSpec, 0, n)
		for i := 0; i < n; i++ {
			base := rune(i * 3)
			faces = append(faces, FaceSpec{
				WeightLo: 400, WeightHi: 400, WidthLo: 100, WidthHi: 100,
				Runes: []RuneRange{{Lo: base, Hi: base + 1}}, Policy: PolicySwap,
			})
		}
		if err := e.RegisterFamily(FamilySpec{Name: "Big", Faces: faces}); err != nil {
			t.Fatal(err)
		}
		fam := e.reg.families["Big"]
		// 覆盖集合由区间树给出，规模与 N 无关（这里恒为 1）。
		hits := fam.coveringFaces(0)
		if len(hits) != 1 || hits[0] != 0 {
			t.Fatalf("covering set must be singleton independent of N, got %v", hits)
		}
		cands := coveringCandidates(fam, 0)
		if got := matchFace(matchQuery{weight: 400, width: 100}, cands, e.cfg); got != 0 {
			t.Fatalf("N=%d match winner=%d", n, got)
		}
	}
}

func BenchmarkStab(b *testing.B) {
	n := 1 << 14
	rs := make([]faceRuneRange, n)
	for i := range rs {
		base := rune(i * 4)
		rs[i] = faceRuneRange{lo: base, hi: base + 1, face: i}
	}
	tree := newCentroidTree(rs)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tree.stab(rune((i * 4) % (n * 4)))
	}
}

func BenchmarkShapeSingleRune(b *testing.B) {
	n := 1 << 12
	e, _ := testEngine()
	faces := make([]FaceSpec, 0, n)
	for i := 0; i < n; i++ {
		base := rune(i * 3)
		faces = append(faces, FaceSpec{
			WeightLo: 400, WeightHi: 400, WidthLo: 100, WidthHi: 100,
			Runes: []RuneRange{{Lo: base, Hi: base + 1}}, Policy: PolicySwap,
		})
	}
	if err := e.RegisterFamily(FamilySpec{Name: "Big", Faces: faces}); err != nil {
		b.Fatal(err)
	}
	req := ShapeRequest{Family: "Big", Text: "a", Weight: 400, Width: 100}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.Shape(req)
	}
}
