package seqset

import (
	"math/bits"
	"reflect"
	"testing"
)

func iv(start, end int64) Interval { return Interval{Start: start, End: end} }

func ivs(s *Set) []Interval {
	out := make([]Interval, 0, s.Len())
	for i := 0; i < s.Len(); i++ {
		out = append(out, s.At(i))
	}
	return out
}

func TestSetOps(t *testing.T) {
	type step struct {
		op    string // "add", "addm", "rmp", "rmb", "shrink"
		start int64
		end   int64
		arg   int64
		c     int
		want  []Interval
		total int64
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{
			name: "add keeps order and total",
			steps: []step{
				{op: "add", start: 10, end: 12, want: []Interval{iv(10, 12)}, total: 3},
				{op: "add", start: 1, end: 4, want: []Interval{iv(1, 4), iv(10, 12)}, total: 7},
				{op: "add", start: 6, end: 6, want: []Interval{iv(1, 4), iv(6, 6), iv(10, 12)}, total: 8},
			},
		},
		{
			name: "remove point splits middle, edges and singleton",
			steps: []step{
				{op: "add", start: 1, end: 9, want: []Interval{iv(1, 9)}, total: 9},
				{op: "rmp", arg: 5, want: []Interval{iv(1, 4), iv(6, 9)}, total: 8},
				{op: "rmp", arg: 1, want: []Interval{iv(2, 4), iv(6, 9)}, total: 7},
				{op: "rmp", arg: 9, want: []Interval{iv(2, 4), iv(6, 8)}, total: 6},
				{op: "rmp", arg: 3, want: []Interval{iv(2, 2), iv(4, 4), iv(6, 8)}, total: 5},
				{op: "rmp", arg: 2, want: []Interval{iv(4, 4), iv(6, 8)}, total: 4},
			},
		},
		{
			name: "add merged fuses adjacent equal attrs",
			steps: []step{
				{op: "addm", start: 4, end: 6, want: []Interval{iv(4, 6)}, total: 3},
				{op: "addm", start: 1, end: 3, want: []Interval{iv(1, 6)}, total: 6},
				{op: "addm", start: 7, end: 9, want: []Interval{iv(1, 9)}, total: 9},
			},
		},
		{
			name: "add merged keeps different attrs separate",
			steps: []step{
				{op: "addm", start: 1, end: 2, want: []Interval{iv(1, 2)}, total: 2},
				{op: "add", start: 3, end: 4, c: 1, want: []Interval{iv(1, 2), {Start: 3, End: 4, Attr: Attr{C: 1}}}, total: 4},
			},
		},
		{
			name: "remove before drops prefix and splits boundary",
			steps: []step{
				{op: "add", start: 2, end: 3, want: []Interval{iv(2, 3)}, total: 2},
				{op: "add", start: 5, end: 9, want: []Interval{iv(2, 3), iv(5, 9)}, total: 7},
				{op: "add", start: 20, end: 21, want: []Interval{iv(2, 3), iv(5, 9), iv(20, 21)}, total: 9},
				{op: "rmb", arg: 7, want: []Interval{iv(7, 9), iv(20, 21)}, total: 5},
				{op: "rmb", arg: 100, want: nil, total: 0},
			},
		},
		{
			name: "shrink start consumes prefix",
			steps: []step{
				{op: "add", start: 4, end: 9, want: []Interval{iv(4, 9)}, total: 6},
				{op: "shrink", arg: 4, want: []Interval{iv(8, 9)}, total: 2},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s Set
			for i, st := range tc.steps {
				switch st.op {
				case "add":
					s.Add(Interval{Start: st.start, End: st.end, Attr: Attr{C: st.c}})
				case "addm":
					s.AddMerged(iv(st.start, st.end))
				case "rmp":
					idx, ok := s.Find(st.arg)
					if !ok {
						t.Fatalf("step %d: %d not found", i, st.arg)
					}
					s.RemovePointAt(idx, st.arg)
				case "rmb":
					s.RemoveBefore(st.arg)
				case "shrink":
					s.ShrinkStart(0, st.arg)
				}
				got := ivs(&s)
				if len(st.want) == 0 && len(got) == 0 {
					got = nil
				}
				if !reflect.DeepEqual(got, st.want) {
					t.Fatalf("step %d: got %v want %v", i, got, st.want)
				}
				if s.Total() != st.total {
					t.Fatalf("step %d: total=%d want %d", i, s.Total(), st.total)
				}
			}
		})
	}
}

func TestFindMiss(t *testing.T) {
	var s Set
	s.Add(iv(10, 20))
	s.Add(iv(30, 40))
	for _, seq := range []int64{0, 9, 21, 29, 41, 100} {
		if _, ok := s.Find(seq); ok {
			t.Fatalf("seq %d unexpectedly found", seq)
		}
	}
	for _, seq := range []int64{10, 15, 20, 30, 35, 40} {
		if _, ok := s.Find(seq); !ok {
			t.Fatalf("seq %d not found", seq)
		}
	}
}

func TestVisitedLogarithmic(t *testing.T) {
	for _, g := range []int{10, 10000} {
		var s Set
		for i := 0; i < g; i++ {
			s.Add(iv(int64(4*i), int64(4*i+1)))
		}
		s.ResetVisited()
		if _, ok := s.Find(int64(4*(g-1) + 1)); !ok {
			t.Fatalf("g=%d: tail seq not found", g)
		}
		got := s.Visited()
		bound := 2*(bits.Len(uint(g+1))) + 4 // 2*ceil(log2(g+2))+4
		t.Logf("g=%d visited=%d bound=%d (依据: 二分查找探测数不随 g 线性增长)", g, got, bound)
		if got > bound {
			t.Fatalf("g=%d: visited %d exceeds bound %d", g, got, bound)
		}
	}
}
