package merge

import (
	"errors"
	"math"
	"testing"

	"ontology/schema"
)

func TestMergeWorkedExample(t *testing.T) {
	a := mkHist("lat", []int64{10, 20, 50, 100}, []uint64{3, 2, 4, 1, 0}, 500)
	b := mkHist("lat", []int64{20, 100}, []uint64{1, 8, 3}, 700)
	resetComparisons()
	m, err := Merge(a, b)
	cmps := comparisonCount()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入 a=%+v b=%+v\n输出=%+v 比较次数=%d（上界 %d）", a, b, m, cmps, len(a.Bounds)+len(b.Bounds))
	wantB := []int64{20, 100}
	wantC := []uint64{6, 13, 3}
	for i := range wantB {
		if m.Bounds[i] != wantB[i] || m.Counts[i] != wantC[i] {
			t.Fatalf("桶 %d: got (%d,%d) want (%d,%d)", i, m.Bounds[i], m.Counts[i], wantB[i], wantC[i])
		}
	}
	if m.Counts[2] != 3 || m.Sum != 1200 {
		t.Fatalf("溢出桶或 Sum 错误: %+v", m)
	}
	if cmps > len(a.Bounds)+len(b.Bounds) {
		t.Fatalf("比较次数 %d > %d", cmps, len(a.Bounds)+len(b.Bounds))
	}
	// 朴素模拟对照
	pa := naiveProject(a.Bounds, a.Counts, wantB)
	pb := naiveProject(b.Bounds, b.Counts, wantB)
	for i := range wantC {
		if pa[i]+pb[i] != wantC[i] {
			t.Fatalf("朴素模拟不符: %d+%d != %d", pa[i], pb[i], wantC[i])
		}
	}
	// 总计数与 Sum 守恒
	var nA, nB, nM uint64
	for i := range a.Counts {
		nA += a.Counts[i]
	}
	for i := range b.Counts {
		nB += b.Counts[i]
	}
	for i := range m.Counts {
		nM += m.Counts[i]
	}
	if nA+nB != nM {
		t.Fatal("合并未保持总计数")
	}
}

func TestMergeComparisonTiers(t *testing.T) {
	mk := func(n int) (schema.Hist, schema.Hist) {
		ba := make([]int64, n)
		bb := make([]int64, 3)
		ca := make([]uint64, n+1)
		cb := make([]uint64, 4)
		for i := range ba {
			ba[i] = int64((i + 1) * 10)
			ca[i] = 1
		}
		for i := range bb {
			bb[i] = int64((i + 1) * 20)
			cb[i] = 1
		}
		return mkHist("x", ba, ca, 0), mkHist("x", bb, cb, 0)
	}
	for _, n := range []int{64, 3} {
		a, b := mk(n)
		resetComparisons()
		if _, err := Merge(a, b); err != nil {
			t.Fatal(err)
		}
		got := comparisonCount()
		limit := len(a.Bounds) + len(b.Bounds)
		t.Logf("档位 边界数=%d+%d 实际比较=%d 上界=%d", len(a.Bounds), len(b.Bounds), got, limit)
		if got > limit {
			t.Fatalf("比较次数 %d 超过 %d", got, limit)
		}
	}
}

func TestMergeIntersectionEqualsSmallerSide(t *testing.T) {
	big := mkHist("h", []int64{10, 20, 30, 40}, []uint64{1, 1, 1, 1, 1}, 0)
	small := mkHist("h", []int64{20, 40}, []uint64{5, 7, 2}, 0)
	m, err := Merge(big, small)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("交集等于较小一侧: %+v（朴素投影 big=%v）", m, naiveProject(big.Bounds, big.Counts, small.Bounds))
	if len(m.Bounds) != 2 || m.Bounds[0] != 20 || m.Bounds[1] != 40 {
		t.Fatalf("边界应为较小侧副本: %v", m.Bounds)
	}
	if m.Counts[0] != 7 || m.Counts[1] != 9 || m.Counts[2] != 3 {
		t.Fatalf("上移并桶错误 counts=%v（应 7,9,3）", m.Counts)
	}
}

func TestMergeNonAdjacentChainNoCommonBounds(t *testing.T) {
	// 版本链 v1=[10,20,50] ⊃ v2=[20] ⊂ v3=[30,70]：v1 与 v3 无公共边界。
	v1 := mkHist("z", []int64{10, 20, 50}, []uint64{1, 2, 3, 4}, 0)
	v3 := mkHist("z", []int64{30, 70}, []uint64{5, 6, 7}, 0)
	_, err := Merge(v1, v3)
	t.Logf("v1 与 v3 合并 err=%v（判定：交集为空 -> 不兼容）", err)
	if !errors.Is(err, schema.ErrIncompatible) {
		t.Fatalf("首尾无公共边界应报不兼容, got %v", err)
	}
	v2 := mkHist("z", []int64{20}, []uint64{9, 1}, 0)
	if _, err := Merge(v1, v2); err != nil {
		t.Fatalf("相邻版本应可合并: %v", err)
	}
}

func TestMergeErrorOrder(t *testing.T) {
	invalid := mkHist("p", []int64{10}, []uint64{0}, 0) // Counts 长度错
	other := mkHist("q", []int64{10}, []uint64{0, 0}, 0)
	same1 := mkHist("p", []int64{10}, []uint64{1, 0}, 0)
	same2NoCommon := mkHist("p", []int64{99}, []uint64{1, 0}, 0)
	if _, err := Merge(invalid, other); !errors.Is(err, schema.ErrInvalidArgument) {
		t.Fatalf("参数非法优先: %v", err)
	}
	if _, err := Merge(mkHist("p", []int64{10}, []uint64{1, 0}, 0), other); !errors.Is(err, schema.ErrNameMismatch) {
		t.Fatalf("其次名字错误: %v", err)
	}
	if _, err := Merge(same1, same2NoCommon); !errors.Is(err, schema.ErrIncompatible) {
		t.Fatalf("再次不兼容: %v", err)
	}
	over := mkHist("p", []int64{10}, []uint64{math.MaxInt64, 0}, 0)
	if _, err := Merge(over, mkHist("p", []int64{10}, []uint64{1, 0}, 0)); !errors.Is(err, schema.ErrOverflow) {
		t.Fatalf("最后溢出: %v", err)
	}
	sumOver := mkHist("p", []int64{10}, []uint64{0, 0}, math.MaxInt64)
	if _, err := Merge(sumOver, mkHist("p", []int64{10}, []uint64{0, 1}, 1)); !errors.Is(err, schema.ErrOverflow) {
		t.Fatalf("Sum 溢出: %v", err)
	}
}

func TestMergeOverflowKeepsInputs(t *testing.T) {
	a := mkHist("p", []int64{10, 20}, []uint64{math.MaxInt64 - 2, 1, 0}, 0)
	b := mkHist("p", []int64{10, 20}, []uint64{5, 1, 0}, 0)
	aSnap := a
	_, err := Merge(a, b)
	if !errors.Is(err, schema.ErrOverflow) {
		t.Fatalf("期望溢出, got %v", err)
	}
	if a.Counts[0] != aSnap.Counts[0] || a.Name != aSnap.Name {
		t.Fatal("溢出时输入被修改")
	}
}

func TestMergeAllPermutationAndAssociativity(t *testing.T) {
	h1 := mkHist("g", []int64{10, 20, 50}, []uint64{1, 2, 3, 4}, 100)
	h2 := mkHist("g", []int64{20, 50}, []uint64{5, 6, 7}, 200)
	h3 := mkHist("g", []int64{20, 30, 50, 90}, []uint64{8, 9, 10, 11, 12}, 300)
	all, err := MergeAll([]schema.Hist{h1, h2, h3})
	if err != nil {
		t.Fatal(err)
	}
	perm, err := MergeAll([]schema.Hist{h3, h1, h2})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MergeAll=%+v；排列后=%+v", all, perm)
	if !equalHist(all, perm) {
		t.Fatal("MergeAll 结果与排列有关")
	}
	m12, _ := Merge(h1, h2)
	assoc, err := Merge(m12, h3)
	if err != nil {
		t.Fatal(err)
	}
	m23, _ := Merge(h2, h3)
	assoc2, err := Merge(h1, m23)
	if err != nil {
		t.Fatal(err)
	}
	if !equalHist(assoc, all) || !equalHist(assoc2, all) {
		t.Fatalf("MergeAll 与逐个 Merge 不一致:\nMergeAll=%+v\n(m12)+3=%+v\n1+(m23)=%+v", all, assoc, assoc2)
	}
	var n uint64
	for _, c := range all.Counts {
		n += c
	}
	if n != 78 || all.Sum != 600 {
		t.Fatalf("总量守恒错误 N=%d Sum=%d", n, all.Sum)
	}
	if _, err := MergeAll(nil); !errors.Is(err, schema.ErrInvalidArgument) {
		t.Fatalf("空列表参数非法: %v", err)
	}
	if _, err := MergeAll([]schema.Hist{h1, mkHist("g2", []int64{20}, []uint64{1, 0}, 0)}); !errors.Is(err, schema.ErrNameMismatch) {
		t.Fatal("MergeAll 名字错误")
	}
}
