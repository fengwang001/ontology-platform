package lsn

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func TestAddRejectsAndOrder(t *testing.T) {
	cases := []struct {
		name string
		seq  []int64
		want []int64 // 期望的升序内容
		err  error   // 期望最终一次 Add 的错误
	}{
		{"empty", nil, []int64{}, nil},
		{"single", []int64{10}, []int64{10}, nil},
		{"sorted", []int64{10, 13, 15}, []int64{10, 13, 15}, nil},
		{"shuffled", []int64{15, 10, 13, 12}, []int64{10, 12, 13, 15}, nil},
		{"duplicate", []int64{10, 10}, []int64{10}, ErrDuplicate},
		{"dup-after-many", []int64{10, 13, 15, 13}, []int64{10, 13, 15}, ErrDuplicate},
		{"negative", []int64{-1}, []int64{}, ErrNegative},
		{"zero-ok", []int64{0}, []int64{0}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New()
			var err error
			for _, v := range c.seq {
				err = s.Add(v)
			}
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if got := s.vals; !reflect.DeepEqual(got, c.want) {
				t.Fatalf("vals = %v, want %v", got, c.want)
			}
			if len(s.vals) != len(s.exist) {
				t.Fatalf("slice/map desync: %d vs %d", len(s.vals), len(s.exist))
			}
		})
	}
}

func TestRejectedAddLeavesNoTrace(t *testing.T) {
	s := New()
	for _, v := range []int64{10, 13, 15} {
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	snap := append([]int64(nil), s.vals...)
	hBefore := s.Holes()
	for _, bad := range []int64{-3, 10, -1, 13} {
		if err := s.Add(bad); err == nil {
			t.Fatalf("Add(%d) should fail", bad)
		}
		if !reflect.DeepEqual(s.vals, snap) || !reflect.DeepEqual(s.Holes(), hBefore) {
			t.Fatalf("state changed after rejected Add(%d): %v", bad, s.vals)
		}
	}
}

func TestIndexOfAndAt(t *testing.T) {
	s := New()
	for _, v := range []int64{10, 12, 13, 15, 17, 18, 20} {
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		v    int64
		rank int
		ok   bool
	}{
		{10, 0, true}, {12, 1, true}, {20, 6, true},
		{11, 1, false}, {19, 6, false}, {100, 7, false},
	}
	for _, c := range cases {
		rank, ok := s.IndexOf(c.v)
		if rank != c.rank || ok != c.ok {
			t.Fatalf("IndexOf(%d)=(%d,%v), want (%d,%v)", c.v, rank, ok, c.rank, c.ok)
		}
		if ok {
			if got, ok := s.AtOK(rank); !ok || got != c.v {
				t.Fatalf("AtOK(%d)=(%d,%v), want %d", rank, got, ok, c.v)
			}
		}
	}
	if _, ok := s.AtOK(-1); ok {
		t.Fatal("AtOK(-1) must fail")
	} else if _, ok := s.AtOK(s.Len()); ok {
		t.Fatal("AtOK(n) must fail")
	}
}

func TestHoles(t *testing.T) {
	cases := []struct {
		seq  []int64
		want []int64
	}{
		{nil, []int64{}},
		{[]int64{10}, []int64{}},
		{[]int64{10, 13}, []int64{11, 12}},
		{[]int64{15, 10, 13, 12}, []int64{11, 14}},
		{[]int64{10, 11, 12}, []int64{}},
		{[]int64{0, 5}, []int64{1, 2, 3, 4}},
	}
	for _, c := range cases {
		s := New()
		for _, v := range c.seq {
			if err := s.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		if got := s.Holes(); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("seq=%v holes=%v want %v", c.seq, got, c.want)
		}
	}
}

// TestProbeIsLogarithmic 同包内部测试直接读非导出字段 probe：比较次数不随 m 线性增长。
func TestProbeIsLogarithmic(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		r := rand.New(rand.NewSource(int64(m)))
		perm := r.Perm(m) // 互异 LSN、随机到达顺序
		for _, p := range perm {
			if err := s.Add(int64(p) * 7); err != nil {
				t.Fatal(err)
			}
		}
		target := int64(perm[0]) * 7
		rank, ok := s.IndexOf(target)
		if !ok {
			t.Fatalf("m=%d target %d not found", m, target)
		}
		bound := int(math.Ceil(math.Log2(float64(m)))) + 2
		if p := int(s.probe.Load()); p > bound || p >= 32 {
			t.Fatalf("m=%d probe=%d bound=%d (must be <32 constant)", m, p, bound)
		}
		if got, _ := s.AtOK(rank); got != target {
			t.Fatalf("round-trip rank %d -> %d, want %d", rank, got, target)
		}
		t.Logf("m=%d probe=%d bound=%d", m, s.probe.Load(), bound)
	}
}
