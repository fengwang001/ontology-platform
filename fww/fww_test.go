package fww

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"ontology/reg"
)

func viewOf(s *Store) (v map[string]string, d int64) {
	s.Snapshot(func(m map[string]string, n int64) { v, d = m, n })
	return
}

var sixW = []Write{{Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 3, Val: "b"}, {Key: "K", Seq: 10, Val: "c"}, {Key: "K", Seq: 1, Val: "d"}, {Key: "K", Seq: 5, Val: "e"}, {Key: "K", Seq: 8, Val: "f"}}
var sixCh = [][]Change{{{true, "K", 7, "a"}}, {{false, "K", 7, "a"}, {true, "K", 3, "b"}}, {}, {{false, "K", 3, "b"}, {true, "K", 1, "d"}}, {}, {}}

// TestRandomOrderMatchesBatch pins invariant 1 over random arrival orders.
func TestRandomOrderMatchesBatch(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		rnd, all := rand.New(rand.NewSource(seed)), []Write{}
		for k := 0; k < 8; k++ {
			for _, p := range rnd.Perm(12)[:6] {
				all = append(all, Write{Key: fmt.Sprintf("k%d", k), Seq: int64(p) + 1, Val: fmt.Sprintf("v%d", p)})
			}
		}
		ord := append([]Write(nil), all...)
		rnd.Shuffle(len(ord), func(i, j int) { ord[i], ord[j] = ord[j], ord[i] })
		s := New()
		if _, err := s.Feed(ord); err != nil { // slice order is arrival order
			t.Fatal(err)
		}
		best := map[string]reg.Entry{} // independent batch recomputation
		for _, w := range all {
			if e, ok := best[w.Key]; !ok || w.Seq < e.Seq {
				best[w.Key] = reg.Entry{Seq: w.Seq, Val: w.Val}
			}
		}
		got, _ := viewOf(s)
		for key, e := range best {
			if got[key] != e.Val {
				t.Fatalf("seed %d %s: %q != %q", seed, key, got[key], e.Val)
			}
		}
	}
}

// TestChangelogPrefixes pins the §3 six steps and invariant 2 (one live value
// per key at every prefix; each "-" matches that live value).
func TestChangelogPrefixes(t *testing.T) {
	s, log := New(), []Change{}
	for i, w := range sixW {
		ch, err := s.Feed([]Write{w})
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if !reflect.DeepEqual(ch, sixCh[i]) {
			t.Fatalf("step %d: %v != %v", i, ch, sixCh[i])
		}
		log = append(log, ch...)
		live := map[string]reg.Entry{}
		for _, c := range log {
			e, ok := live[c.Key]
			if c.Added {
				if ok {
					t.Fatalf("two live values for %s", c.Key)
				}
				live[c.Key] = reg.Entry{Seq: c.Seq, Val: c.Val}
			} else if !ok || e.Seq != c.Seq || e.Val != c.Val {
				t.Fatalf("withdraw (%d,%s) != live %v", c.Seq, c.Val, e)
			} else {
				delete(live, c.Key)
			}
		}
	}
	if v, d := viewOf(s); v["K"] != "d" || d != 3 {
		t.Fatalf("view=%v dropped=%d, want K:d / 3", v, d)
	}
}

// TestSeqMonotonic pins invariant 3: the effective Seq never increases.
func TestSeqMonotonic(t *testing.T) {
	s, prev := New(), int64(1<<62)
	for i, c := range []struct{ in, want int64 }{{9, 9}, {20, 9}, {5, 5}, {6, 5}, {1, 1}} {
		if _, err := s.Feed([]Write{{Key: "K", Seq: c.in, Val: "x"}}); err != nil {
			t.Fatal(err)
		}
		if g := s.m["K"].Current().Seq; g != c.want || g > prev {
			t.Fatalf("step %d: %d want %d and <= %d", i, g, c.want, prev)
		}
		prev = s.m["K"].Current().Seq
	}
}

// TestRejectedBatchAtomic pins invariant 4: a rejected batch leaves no trace.
func TestRejectedBatchAtomic(t *testing.T) {
	s := New()
	if _, err := s.Feed([]Write{{Key: "K", Seq: 3, Val: "a"}, {Key: "Q", Seq: 2, Val: "q"}}); err != nil {
		t.Fatal(err)
	}
	saved, d0 := viewOf(s)
	cases := []struct {
		b    []Write
		want error
	}{
		{[]Write{{Key: "K", Seq: 1, Val: "x"}, {Key: "", Seq: 1, Val: "y"}}, ErrEmptyKey},
		{[]Write{{Key: "K", Seq: 3, Val: "dup"}, {Key: "z", Seq: 1}}, ErrDuplicateSeq},
		{[]Write{{Key: "K", Seq: 0, Val: "bad"}, {Key: "z", Seq: 1}}, ErrNonPositiveSeq},
	}
	for i, c := range cases {
		if ch, err := s.Feed(c.b); !errors.Is(err, c.want) || ch != nil {
			t.Fatalf("case %d: ch=%v err=%v", i, ch, err)
		}
		if v, d := viewOf(s); !reflect.DeepEqual(v, saved) || d != d0 {
			t.Fatalf("case %d left a trace: %v %d", i, v, d)
		}
	}
	if _, err := s.Feed([]Write{{Key: "K", Seq: 1, Val: "new"}}); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	} else if v, _ := viewOf(s); v["K"] != "new" {
		t.Fatalf("post-rejection write lost: %v", v)
	}
}

// TestLookupIsConstant pins §4: key location is O(1); the unexported counter never crosses the API.
func TestLookupIsConstant(t *testing.T) {
	first := -1
	for i, m := range []int{100, 1000, 10000} {
		s, b := New(), make([]Write, m)
		for j := range b {
			b[j] = Write{Key: strconv.Itoa(j), Seq: 1}
		}
		if _, err := s.Feed(b); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Feed([]Write{{Key: "0", Seq: 2}}); err != nil {
			t.Fatal(err)
		}
		if s.lastChecked > 2 || (i > 0 && s.lastChecked != first) {
			t.Fatalf("m=%d checked=%d first=%d, want constant", m, s.lastChecked, first)
		}
		first = s.lastChecked
	}
}
