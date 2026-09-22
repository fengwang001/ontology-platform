package journal

import (
	"errors"
	"testing"
)

func rec() Record { return Record{Kind: KindStep, Index: 1} }

func TestAppendReadOrderAndSeq(t *testing.T) {
	cases := []struct {
		name string
		n    int
	}{
		{"one", 1},
		{"many", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := New(64)
			for i := 0; i < tc.n; i++ {
				if err := j.Append("x", rec()); err != nil {
					t.Fatal(err)
				}
			}
			got := j.Read("x")
			if len(got) != tc.n {
				t.Fatalf("len = %d, want %d", len(got), tc.n)
			}
			for i, r := range got {
				if r.Seq != i {
					t.Fatalf("seq[%d] = %d", i, r.Seq)
				}
			}
			last, ok := j.Last("x")
			if !ok || last.Seq != tc.n-1 {
				t.Fatalf("last = %+v ok=%v", last, ok)
			}
		})
	}
}

func TestIsolationAndLimits(t *testing.T) {
	cases := []struct {
		name     string
		max      int
		appendN  int
		failAt   int
		otherHas bool
	}{
		{"full leaves no half record", 2, 3, 2, true},
		{"within limit", 3, 3, -1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := New(tc.max)
			var err error
			for i := 0; i < tc.appendN; i++ {
				err = j.Append("a", rec())
			}
			if tc.failAt >= 0 && !errors.Is(err, ErrJournalFull) {
				t.Fatalf("err = %v, want ErrJournalFull", err)
			}
			if got := j.Len("a"); got != tc.max {
				t.Fatalf("Len = %d, want %d (no half-write)", got, tc.max)
			}
			j.Append("b", rec())
			if j.Len("b") != 1 {
				t.Fatal("instances are not isolated")
			}
			mut := j.Read("a")
			if len(mut) > 0 {
				mut[0].Index = 99
				if j.Read("a")[0].Index == 99 {
					t.Fatal("Read returned shared backing array")
				}
			}
		})
	}
}

func TestLastMissing(t *testing.T) {
	j := New(4)
	if _, ok := j.Last("ghost"); ok {
		t.Fatal("Last on missing instance must report ok=false")
	}
}
