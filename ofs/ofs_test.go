package ofs

import (
	"errors"
	"fmt"
	"testing"
)

// TestAdvanceAndIdempotence 表驱动核验：乱序 Ack 后的推进结果、
// 重复 Ack（含 < C 的重复）幂等且不改态。
func TestAdvanceAndIdempotence(t *testing.T) {
	// 第三节序列：start=100，Deliver 100..107。
	cases := []struct {
		ack       int64
		committed int64
		pending   []int64 // 已 Ack 但尚未被 [start,C) 覆盖的位点
	}{
		{103, 100, []int64{103}},
		{100, 101, []int64{103}},
		{101, 102, []int64{103}},
		{105, 102, []int64{103, 105}},
		{101, 102, []int64{103, 105}}, // 第 5 步：重复 Ack，无变化
		{102, 104, []int64{105}},
		{107, 104, []int64{105, 107}},
	}
	s := New(100)
	for off := int64(100); off < 108; off++ {
		if err := s.Deliver(off); err != nil {
			t.Fatalf("Deliver(%d): %v", off, err)
		}
	}
	for i, c := range cases {
		if err := s.Ack(c.ack); err != nil {
			t.Fatalf("case %d: Ack(%d): %v", i, c.ack, err)
		}
		if s.Committed() != c.committed {
			t.Fatalf("case %d: C=%d want %d", i, s.Committed(), c.committed)
		}
		got := pendingSet(s)
		if !sameSet(got, c.pending) {
			t.Fatalf("case %d: pending=%v want %v", i, got, c.pending)
		}
	}
	// < C 的 Ack 必然是重复：幂等成功且状态不变。
	before := s.Committed()
	if err := s.Ack(99); err != nil {
		t.Fatalf("Ack below C: %v", err)
	}
	if s.Committed() != before || len(s.acked) != len(pendingSet(s)) {
		t.Fatalf("Ack below C changed state")
	}
}

func pendingSet(s *State) []int64 {
	out := make([]int64, 0, len(s.acked))
	for off := range s.acked {
		out = append(out, off)
	}
	return out
}

func sameSet(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[int64]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

// TestRejectedLeavesNoTrace：Deliver 空洞与 Ack 越界均返回各自哨兵错误，
// 且拒绝后 delivered/committed/acked 完全不变，分区仍可正常使用。
func TestRejectedLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name string
		fn   func(s *State) error
		want error
	}{
		{"deliver-gap-ahead", func(s *State) error { return s.Deliver(1) }, ErrDeliverGap},
		{"deliver-gap-back", func(s *State) error { return s.Deliver(-1) }, ErrDeliverGap},
		{"ack-undefined", func(s *State) error { return s.Ack(0) }, ErrAckOutOfRange},
		{"ack-high", func(s *State) error { return s.Ack(5) }, ErrAckOutOfRange},
	}
	s := New(0)
	for _, c := range cases {
		d, cm, n := s.Delivered(), s.Committed(), len(s.acked)
		err := c.fn(s)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		if c.want != nil && (s.Delivered() != d || s.Committed() != cm || len(s.acked) != n) {
			t.Fatalf("%s: state changed after rejection", c.name)
		}
	}
	// 被拒后仍可正常推进。
	if err := s.Deliver(0); err != nil || s.Ack(0) != nil || s.Committed() != 1 {
		t.Fatalf("partition unusable after rejections")
	}
}

// TestAdvanceCheckComplexity：Ack 非 C 位点时检查数为与 m 无关的常数；
// Ack C 时检查数不超过真正越过的位点数加常数，证明不做整表重扫。
func TestAdvanceCheckComplexity(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			s := New(0)
			for off := int64(0); off <= m; off++ { // 在途 m+1 个：0..m
				if err := s.Deliver(off); err != nil {
					t.Fatal(err)
				}
			}
			for off := int64(1); off < m; off++ { // C=0 未 Ack，1..m-1 已 Ack，m 未 Ack
				if err := s.Ack(off); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Ack(m); err != nil { // 推进点不在 C 上
				t.Fatal(err)
			}
			if s.Committed() != 0 { // C 未被 Ack，推进绝不能发生
				t.Fatalf("Ack(%d) advanced C to %d, want 0", m, s.Committed())
			}
			if s.lastChecks != 1 { // 只检查 C 一个位点即停，与 m 无关
				t.Fatalf("Ack(%d) checks=%d, want constant 1", m, s.lastChecks)
			}
			if err := s.Ack(0); err != nil { // Ack C：越过 m+1 个位点
				t.Fatal(err)
			}
			if s.Committed() != m+1 {
				t.Fatalf("C=%d want %d", s.Committed(), m+1)
			}
			if int64(s.lastChecks) != m+2 { // 恰为越过的 m+1 个 + 1 个停止检查
				t.Fatalf("Ack(C) checks=%d, want exactly %d (advanced+1)", s.lastChecks, m+2)
			}
		})
	}
}
