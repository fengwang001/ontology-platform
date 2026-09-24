package lsn

import (
	"errors"
	"math/bits"
	"slices"
	"testing"
)

// shuffled 用 LCG 生成 [0,m) 的确定伪随机排列（随机到达顺序）。
func shuffled(m int) []int64 {
	out := make([]int64, m)
	seed := uint64(0x9e3779b9)
	for i := range out {
		out[i] = int64(i)
	}
	for i := m - 1; i > 0; i-- {
		seed = seed*6364136223846793005 + 1442695040888963407
		j := int(seed>>33) % (i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// 复杂度：定位比较个数不随 m 线性增长（二分而非扫描），恒 < 32。
// 同包内部测试，直接读非导出字段 lastCmps。
func TestLocateComparesSublinear(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := New()
		for _, v := range shuffled(m) {
			if err := s.Add(v * 2); err != nil {
				t.Fatalf("m=%d add %d: %v", m, v, err)
			}
		}
		for _, target := range []int64{0, int64(m/3) * 2, int64(m-1) * 2} {
			if _, err := s.IndexOf(target); err != nil {
				t.Fatalf("m=%d find %d: %v", m, target, err)
			}
			if got, limit := s.lastCmps, int64(bits.Len(uint(m-1)))+2; got > limit || got >= 32 {
				t.Fatalf("m=%d target=%d cmps=%d, want <=%d 且 <32", m, target, got, limit)
			}
		}
	}
}

// 失败不留痕：负号/重复/未知/越界之后，集合与计数器都不得改变。
func TestFailureLeavesNoTrace(t *testing.T) {
	s := New()
	for _, v := range []int64{10, 13, 15} {
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.IndexOf(13); err != nil { // 让计数器取一个已知非零值
		t.Fatal(err)
	}
	cmps0, vals0 := s.lastCmps, slices.Clone(s.vals)
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"negative", func() error { return s.Add(-1) }, ErrNegative},
		{"duplicate", func() error { return s.Add(13) }, ErrDuplicate},
		{"unknown", func() error { _, e := s.IndexOf(11); return e }, ErrUnknown},
		{"oor-high", func() error { _, e := s.ValueAt(3); return e }, ErrOutOfRange},
		{"oor-low", func() error { _, e := s.ValueAt(-1); return e }, ErrOutOfRange},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		if s.lastCmps != cmps0 || !slices.Equal(s.vals, vals0) {
			t.Fatalf("%s: state changed after reject", c.name)
		}
	}
}
