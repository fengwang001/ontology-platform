package replay

import (
	"errors"
	"math/rand"
	"testing"
)

func TestRangeBasicAndBoundaries(t *testing.T) {
	dir := t.TempDir()
	writeSegments(t, dir, 9, 3) // 3 段，每段 3 条，序号 0..8
	buildIndexes(t, dir, 2)
	cases := []struct {
		name     string
		from, to uint64
		count    int
		first    uint64
		last     uint64
	}{
		{"whole one segment", 3, 5, 3, 3, 5},
		{"cross three segments", 1, 7, 7, 1, 7},
		{"before min", 0, 2, 3, 0, 2},
		{"from below zero", 0, 0, 1, 0, 0},
		{"to beyond max", 6, 100, 3, 6, 8},
		{"single event", 4, 4, 1, 4, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := New(dir, 2).Range(tc.from, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Events) != tc.count {
				t.Fatalf("count=%d want %d", len(res.Events), tc.count)
			}
			if res.Events[0].Seq != tc.first || res.Events[len(res.Events)-1].Seq != tc.last {
				t.Fatalf("range got %d..%d want %d..%d",
					res.Events[0].Seq, res.Events[len(res.Events)-1].Seq, tc.first, tc.last)
			}
			for i := 1; i < len(res.Events); i++ {
				if res.Events[i].Seq != res.Events[i-1].Seq+1 {
					t.Fatalf("gap at %d", i)
				}
			}
		})
	}
}

func TestBadRangeAndEmpty(t *testing.T) {
	if _, err := New(t.TempDir(), 4).Range(5, 1); !errors.Is(err, ErrBadRange) {
		t.Fatalf("err=%v want ErrBadRange", err)
	}
	dir := t.TempDir()
	res, err := New(dir, 4).Range(0, 10)
	if err != nil || len(res.Events) != 0 || res.Stats.Bytes != 0 {
		t.Fatalf("empty log: ev=%d bytes=%d err=%v", len(res.Events), res.Stats.Bytes, err)
	}
}

func TestSkipBound(t *testing.T) {
	const total, n = 100000, 128
	dir := t.TempDir()
	writeSegments(t, dir, total, total)
	buildIndexes(t, dir, n)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		from := uint64(rng.Intn(total))
		res, err := New(dir, n).Range(from, from+10)
		if err != nil {
			t.Fatal(err)
		}
		if uint64(res.Stats.Skipped) >= n {
			t.Fatalf("from=%d skipped=%d >= N=%d", from, res.Stats.Skipped, n)
		}
		if res.Events[0].Seq != from {
			t.Fatalf("first=%d want %d", res.Events[0].Seq, from)
		}
	}
}
