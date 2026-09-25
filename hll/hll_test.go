package hll

import (
	"fmt"
	"math"
	"testing"
)

func addN(t *testing.T, s *Sketch, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Add(fmt.Sprintf("%s-%d", prefix, i)); err != nil {
			t.Fatal(err)
		}
	}
}

func eq(a, b []uint8) bool {
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

// 不变量 1：合并 == 逐一添加（逐桶），且交换、幂等。
func TestMergeEqualsUnion(t *testing.T) {
	for _, p := range []int{4, 8, 12} {
		a, _ := New(p)
		b, _ := New(p)
		a2, _ := New(p)
		b2, _ := New(p)
		whole, _ := New(p)
		for i := 0; i < 1500; i++ {
			k := fmt.Sprintf("m-%d-%d", p, i)
			whole.Add(k)
			if i%2 == 0 {
				a.Add(k)
				a2.Add(k)
			} else {
				b.Add(k)
				b2.Add(k)
			}
		}
		if err := a.Merge(b); err != nil {
			t.Fatal(err)
		}
		if err := b2.Merge(a2); err != nil {
			t.Fatal(err)
		}
		if !eq(a.reg, whole.reg) || !eq(b2.reg, whole.reg) {
			t.Errorf("p=%d: merge != union (or not commutative)", p)
		}
		if err := a.Merge(a); err != nil || !eq(a.reg, whole.reg) {
			t.Errorf("p=%d: merge not idempotent", p)
		}
	}
}

// 不变量 2：估计误差 <= 3σ，σ=1.04/sqrt(m)。
func TestEstimateWithin3Sigma(t *testing.T) {
	cases := []struct{ p, n int }{{4, 80}, {8, 1500}, {12, 20000}, {16, 100000}}
	for _, c := range cases {
		s, _ := New(c.p)
		addN(t, s, fmt.Sprintf("e%d", c.p), c.n)
		est, err := s.Estimate()
		if err != nil {
			t.Fatal(err)
		}
		if rel := math.Abs(est-float64(c.n)) / float64(c.n); rel > 3*1.04/math.Sqrt(float64(uint(1)<<c.p)) {
			t.Errorf("p=%d n=%d: rel err %v > 3σ", c.p, c.n, rel)
		}
	}
}

// 不变量 3：寄存器与估计单调不减。
func TestMonotonic(t *testing.T) {
	for _, p := range []int{4, 8, 12} {
		s, _ := New(p)
		prevReg := make([]uint8, 1<<uint(p))
		prevE := 0.0
		for i := 0; i < 3000; i++ {
			s.Add(fmt.Sprintf("mo-%d-%d", p, i))
			for j := range s.reg {
				if s.reg[j] < prevReg[j] {
					t.Fatalf("p=%d: register %d decreased", p, j)
				}
			}
			e, _ := s.Estimate()
			if e < prevE {
				t.Fatalf("p=%d: estimate decreased %v -> %v", p, prevE, e)
			}
			prevE = e
			copy(prevReg, s.reg)
		}
	}
}

// 复杂度：Estimate 实际读取的寄存器个数恒为 0（O(1)，不随 m 增长）。
func TestEstimateReadsZeroRegisters(t *testing.T) {
	for _, p := range []int{4, 8, 12, 16} {
		s, _ := New(p)
		addN(t, s, "z", 3000)
		if _, err := s.Estimate(); err != nil {
			t.Fatal(err)
		}
		if got := s.estReads.Load(); got != 0 {
			t.Errorf("p=%d: Estimate read %d registers, want 0", p, got)
		}
	}
}
