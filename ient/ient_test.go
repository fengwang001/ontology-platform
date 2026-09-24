package ient

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

// TestRangeCheckedLogBound 造 m 个索引项后执行只命中 1 个结果的 Range，
// 断言检查个数不随 m 线性增长（对数量级上界），证明按 (F,PK) 有序定位。
func TestRangeCheckedLogBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := &Set{}
		perm := rand.Perm(m) // 每个主键独占一个随机 F
		for i, f := range perm {
			s.Insert(int64(f), fmt.Sprintf("k%05d", i))
		}
		target := int64(perm[m/2])
		if got := s.Range(target, target+1); len(got) != 1 {
			t.Fatalf("m=%d: want exactly 1 hit, got %v", m, got)
		}
		if bound := 2*math.Log2(float64(m)) + 3; float64(s.lastChecked.Load()) > bound {
			t.Fatalf("m=%d: checked %d items, exceeds log bound %.1f", m, s.lastChecked.Load(), bound)
		}
	}
}

// TestSetRangeTable 表驱动：有序插入后的等值/范围定位与删除。
func TestSetRangeTable(t *testing.T) {
	newSet := func() *Set {
		s := &Set{}
		for _, it := range []Item{{5, "b"}, {5, "a"}, {8, "d"}, {8, "c"}, {-3, "z"}} {
			s.Insert(it.F, it.PK)
		}
		return s
	}
	tests := []struct {
		name   string
		lo, hi int64
		want   []string
	}{
		{"eq group", 5, 6, []string{"a", "b"}},
		{"hi excluded", 5, 8, []string{"a", "b"}},
		{"full span", -3, 9, []string{"a", "b", "c", "d", "z"}},
		{"empty lo==hi", 8, 8, nil},
		{"empty lo>hi", 9, 5, nil},
		{"below all", -10, -3, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newSet().Range(tt.lo, tt.hi); !slices.Equal(got, tt.want) {
				t.Fatalf("Range(%d,%d)=%v want %v", tt.lo, tt.hi, got, tt.want)
			}
		})
	}
	s := newSet()
	if got := s.Eq(8); !slices.Equal(got, []string{"c", "d"}) {
		t.Fatalf("Eq(8)=%v", got)
	}
	s.Delete(5, "a")
	if got := s.Eq(5); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("after delete Eq(5)=%v", got)
	}
	s.Delete(-3, "z")
	if got := s.Range(-10, 100); !slices.Equal(got, []string{"b", "c", "d"}) {
		t.Fatalf("after deletes Range=%v", got)
	}
}
