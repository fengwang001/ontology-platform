package ontology

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func snapEqual(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMergeMismatch 异参数 Merge 必须报 ErrMismatchedSpec 且指出两边 (lo,hi,n)。
func TestMergeMismatch(t *testing.T) {
	a, _ := NewHistogram(0, 1, 3)
	b, _ := NewHistogram(0, 1, 4)
	c, _ := NewHistogram(-1, 2, 7)
	d, _ := NewHistogram(0, 2, 3)

	for _, other := range []*Histogram{b, c, d} {
		_, err := a.Merge(other)
		if !errors.Is(err, ErrMismatchedSpec) {
			t.Fatalf("want ErrMismatchedSpec, got %v", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "left=(0,1,3)") || !strings.Contains(msg, "right=") {
			t.Fatalf("错误信息未指出两边参数: %q", msg)
		}
	}
}

// TestMergeSums 同参数 Merge 逐桶及溢出、跳过数相加，且两个源都不被修改。
func TestMergeSums(t *testing.T) {
	a, _ := NewHistogram(0, 1, 3)
	b, _ := NewHistogram(0, 1, 3)

	_ = a.Add(0.1)
	_ = a.Add(0.9)
	_ = a.Add(2) // over
	_ = a.Add(math.NaN())
	_ = b.Add(0.1)
	_ = b.Add(-1) // under

	beforeA := a.Snapshot()
	beforeB := b.Snapshot()

	m, err := a.Merge(b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	s := m.Snapshot()
	wantCounts := []uint64{2, 0, 1}
	if !snapEqual(s.Counts, wantCounts) {
		t.Fatalf("counts: want %v, got %v", wantCounts, s.Counts)
	}
	if s.Under != 1 || s.Over != 1 || s.Skip != 1 {
		t.Fatalf("溢出/跳过相加错误: under=%d over=%d skip=%d", s.Under, s.Over, s.Skip)
	}
	if !s.IdentityHolds() {
		t.Fatal("合并后恒等式不成立")
	}

	// Merge 不得修改任何一个源：两个源的快照与合并前逐元素相同。
	if after := b.Snapshot(); !snapEqual(after.Counts, beforeB.Counts) ||
		after.Under != beforeB.Under || after.Over != beforeB.Over ||
		after.Skip != beforeB.Skip || after.Added != beforeB.Added {
		t.Fatal("Merge 修改了右源")
	}
	if got := b.Buckets(); !snapEqual(got, beforeB.Counts) {
		t.Fatalf("右源 Buckets() 被改变: before=%v after=%v", beforeB.Counts, got)
	}
	if after := a.Snapshot(); !snapEqual(after.Counts, beforeA.Counts) ||
		after.Under != beforeA.Under || after.Over != beforeA.Over ||
		after.Skip != beforeA.Skip || after.Added != beforeA.Added {
		t.Fatal("Merge 修改了左源")
	}
}
