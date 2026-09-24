package ient

import (
	"fmt"
	"math/bits"
	"math/rand"
	"slices"
	"testing"
)

// TestRangeCheckedLogBound 证明 Range 按 (F,PK) 有序定位而非整表扫描：
// 只命中 1 个结果时，检查个数不随 m 线性增长，不超过 2*log2(m)+3。
// 计数器是非导出字段，本测试与实现同包直接读取，不经由导出接口。
func TestRangeCheckedLogBound(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		rng := rand.New(rand.NewSource(int64(m)))
		s := New()
		seen := map[int64]int{}
		for i := 0; i < m; i++ {
			f := rng.Int64() // 随机 F，主键互异
			s.Add(f, fmt.Sprintf("pk%06d", i))
			seen[f]++
		}
		target := int64(-1)
		for f, n := range seen { // 找一个恰好出现一次的 F
			if n == 1 {
				target = f
				break
			}
		}
		got := s.Range(target, target+1)
		if len(got) != 1 {
			t.Fatalf("m=%d: want 1 hit, got %v", m, got)
		}
		if bound := 2*(bits.Len(uint(m))-1) + 3; s.checked > bound {
			t.Fatalf("m=%d: checked %d items, exceeds log bound %d", m, s.checked, bound)
		}
	}
}

// TestSetOps 表驱动：插入去重、删除、Eq/Range 有序与半开区间。
func TestSetOps(t *testing.T) {
	s := New()
	for _, op := range []struct {
		add   bool
		f     int64
		pk    string
		eqF   int64
		want  []string
		rLo   int64
		rHi   int64
		wantR []string
	}{
		{true, 5, "b", 5, []string{"b"}, 5, 6, []string{"b"}},
		{true, 5, "a", 5, []string{"a", "b"}, 0, 5, []string{}},
		{true, 5, "a", 5, []string{"a", "b"}, 5, 5, []string{}}, // 重复插入去重；lo>=hi 为空
		{true, 8, "c", 8, []string{"c"}, 5, 9, []string{"a", "b", "c"}},
		{false, 5, "a", 5, []string{"b"}, 5, 8, []string{"b"}}, // hi=8 不含 c
		{false, 5, "zz", 5, []string{"b"}, 6, 8, []string{}},   // 删不存在项无操作
	} {
		if op.add {
			s.Add(op.f, op.pk)
		} else {
			s.Del(op.f, op.pk)
		}
		if got := s.Eq(op.eqF); !slices.Equal(got, op.want) {
			t.Fatalf("Eq(%d)=%v, want %v", op.eqF, got, op.want)
		}
		if got := s.Range(op.rLo, op.rHi); !slices.Equal(got, op.wantR) {
			t.Fatalf("Range(%d,%d)=%v, want %v", op.rLo, op.rHi, got, op.wantR)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
