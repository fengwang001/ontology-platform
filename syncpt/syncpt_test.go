package syncpt

import (
	"errors"
	"testing"
)

func TestAddAndErrors(t *testing.T) {
	cases := []struct {
		name string
		seq  [][2]int64
		err  error // 对最后一个点的判定
	}{
		{"ascending", [][2]int64{{10, 100}, {20, 200}}, nil},
		{"out-of-order-ok", [][2]int64{{600, 5410}, {200, 5000}}, nil},
		{"dup-k", [][2]int64{{20, 8990}, {20, 9000}}, ErrDupSync},
		{"skew-right", [][2]int64{{20, 8990}, {30, 8985}}, ErrSkew},
		{"skew-equal-w-right", [][2]int64{{20, 8990}, {30, 8990}}, ErrSkew},
		{"insert-middle-ok", [][2]int64{{10, 100}, {30, 300}, {20, 250}}, nil},
		{"skew-left-bad", [][2]int64{{10, 100}, {30, 300}, {20, 99}}, ErrSkew},
		{"skew-equal-w-left", [][2]int64{{10, 100}, {30, 300}, {20, 100}}, ErrSkew},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			var err error
			for i, p := range tc.seq {
				err = s.Add(p[0], p[1])
				if i < len(tc.seq)-1 && err != nil {
					t.Fatalf("unexpected early error: %v", err)
				}
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
		})
	}
}

func TestRejectedAddKeepsSet(t *testing.T) {
	s := New()
	if err := s.Add(10, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(10, 200); !errors.Is(err, ErrDupSync) {
		t.Fatalf("dup: %v", err)
	}
	if err := s.Add(20, 99); !errors.Is(err, ErrSkew) {
		t.Fatalf("skew: %v", err)
	}
	if s.Len() != 1 || s.At(0).K != 10 || s.At(0).W != 100 {
		t.Fatalf("set mutated by rejected adds: %+v", s.pts)
	}
}

func TestInterp(t *testing.T) {
	p, n := Point{K: 200, W: 5000}, Point{K: 600, W: 5410}
	cases := []struct {
		k, want int64
	}{
		{250, 5051}, // floor(50*410/400)=floor(51.25)=51
		{200, 5000},
		{600, 5410},
		{201, 5001}, // floor(410/400)=1
		{599, 5409}, // 5000+floor(399*410/400)=5000+408=5408? 399*410=163590/400=408.975 ->5408
	}
	wants := []int64{5051, 5000, 5410, 5001, 5408}
	for i, tc := range cases {
		if got := Interp(tc.k, p, n); got != wants[i] {
			t.Fatalf("Interp(%d)=%d want %d", tc.k, got, wants[i])
		}
	}
}

func TestInterpBigNoOverflow(t *testing.T) {
	// 乘积达 1e9*1e15=1e24，远超 int64；结果仍应精确。
	p := Point{K: 0, W: 0}
	n := Point{K: 1_000_000_000, W: 1_000_000_000_000_000}
	if got := Interp(500_000_000, p, n); got != 500_000_000_000_000 {
		t.Fatalf("big interp = %d", got)
	}
	// 不能整除时向下取整：k=1, 斜率=1e6 -> 恰整除；k=1, denom=3 场景
	p2 := Point{K: 0, W: 0}
	n2 := Point{K: 3, W: 1_000_000_000_000_000}
	if got := Interp(2, p2, n2); got != 666_666_666_666_666 {
		t.Fatalf("floor interp = %d", got)
	}
}

func TestSpan(t *testing.T) {
	s := New()
	for _, p := range [][2]int64{{200, 5000}, {600, 5410}} {
		if err := s.Add(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	prev, pok, next, nok, exact := s.Span(600)
	if !exact || !pok || !nok || next.W != 5410 || prev.W != 5410 {
		t.Fatalf("exact span wrong: %+v %+v %v", prev, next, exact)
	}
	prev, pok, next, nok, exact = s.Span(250)
	if exact || !pok || !nok || prev.K != 200 || next.K != 600 {
		t.Fatalf("middle span wrong")
	}
	_, pok, _, nok, exact = s.Span(700)
	if !pok || nok || exact {
		t.Fatalf("after-last span wrong: pok=%v nok=%v", pok, nok)
	}
	_, pok, next, nok, exact = s.Span(100)
	if pok || !nok || exact || next.K != 200 {
		t.Fatalf("before-first span wrong")
	}
}
