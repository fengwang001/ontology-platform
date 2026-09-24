package dep

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func naiveDepth(ws [][]string) []int {
	d := make([]int, len(ws))
	for i := range ws {
		top := 0
		for j := 0; j < i; j++ {
			hit := false
			for _, k := range ws[i] {
				for _, q := range ws[j] {
					if k == q {
						hit = true
					}
				}
			}
			if hit && d[j] > top {
				top = d[j]
			}
		}
		d[i] = top + 1
	}
	return d
}

func TestDepthMatchesNaive(t *testing.T) {
	fixed := []struct {
		w, r []string
		want int
	}{
		{[]string{"a"}, nil, 1}, {[]string{"b"}, []string{"a"}, 1},
		{[]string{"a", "c"}, nil, 2}, {[]string{"d"}, []string{"c"}, 1},
		{[]string{"b", "d"}, nil, 2}, {[]string{"c"}, []string{"b"}, 3},
		{[]string{"e"}, []string{"a"}, 1}, {[]string{"b", "e"}, nil, 3},
	}
	g := New()
	for i, c := range fixed {
		if err := g.Add(Txn{Seq: int64(i + 1), Writes: c.w, Reads: c.r}); err != nil {
			t.Fatalf("add %d: %v", i+1, err)
		}
		if _, _, d, _ := g.Record(int64(i + 1)); d != c.want {
			t.Fatalf("seq %d depth=%d want %d (reads must be ignored)", i+1, d, c.want)
		}
	}
	if err := g.VerifyDepths(); err != nil {
		t.Fatal(err)
	}
	for seed := int64(0); seed < 40; seed++ {
		rng, g := rand.New(rand.NewSource(seed)), New()
		n := 1 + rng.Intn(80)
		ws := make([][]string, n)
		for i := range ws {
			ws[i] = []string{fmt.Sprintf("k%d", rng.Intn(6))}
			if rng.Intn(2) == 0 {
				ws[i] = append(ws[i], fmt.Sprintf("k%d", rng.Intn(6)))
			}
			if err := g.Add(Txn{Seq: int64(i + 1), Writes: ws[i]}); err != nil {
				t.Fatal(err)
			}
		}
		want := naiveDepth(ws)
		for i := range want {
			if _, _, d, _ := g.Record(int64(i + 1)); d != want[i] {
				t.Fatalf("seed %d seq %d depth=%d naive=%d", seed, i+1, d, want[i])
			}
		}
		if err := g.VerifyDepths(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLastWriterDepsAndDedup(t *testing.T) {
	g := New()
	for i, w := range [][]string{{"a", "a"}, {"a", "b"}, {"a"}} {
		if err := g.Add(Txn{Seq: int64(i + 1), Writes: w}); err != nil {
			t.Fatal(err)
		}
	}
	if w, deps, _, _ := g.Record(2); len(w) != 2 || len(deps) != 1 || deps[0] != 1 {
		t.Fatalf("seq2 writes=%v deps=%v, want dedup writes and dep{1}", w, deps)
	}
	if _, deps, _, _ := g.Record(3); len(deps) != 1 || deps[0] != 2 {
		t.Fatalf("seq3 deps=%v, want only last writer {2}", deps)
	}
}

// TestCheckCounter is white-box: the unexported counter must not grow with m.
func TestCheckCounter(t *testing.T) {
	prev := map[bool]int{}
	for _, m := range []int{100, 1000, 10000} {
		for _, same := range []bool{false, true} {
			g := New()
			for i := 1; i <= m; i++ {
				k := fmt.Sprintf("u%d", i)
				if same {
					k = "x"
				}
				if err := g.Add(Txn{Seq: int64(i), Writes: []string{k}}); err != nil {
					t.Fatal(err)
				}
			}
			fin := []string{fmt.Sprintf("u%d", m), "z"}
			if same {
				fin = []string{"x", "y"}
			}
			if err := g.Add(Txn{Seq: int64(m + 1), Writes: fin}); err != nil {
				t.Fatal(err)
			}
			if g.checks > len(fin)+1 {
				t.Fatalf("m=%d same=%v checks=%d want <= %d", m, same, g.checks, len(fin)+1)
			}
			if p, ok := prev[same]; ok && p != g.checks {
				t.Fatalf("checks grew with m: %d -> %d (same=%v)", p, g.checks, same)
			}
			prev[same] = g.checks
		}
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		txn  Txn
		want error
	}{
		{Txn{Seq: 2, Writes: []string{"a"}}, ErrSeqGap},
		{Txn{Seq: 1, Writes: nil}, ErrEmptyWrites},
		{Txn{Seq: 1, Writes: []string{"a", ""}}, ErrEmptyKey},
		{Txn{Seq: 1, Writes: []string{"a"}, Reads: []string{""}}, ErrEmptyKey},
	}
	for _, c := range cases {
		g := New()
		if err := g.Add(c.txn); !errors.Is(err, c.want) || g.Len() != 0 {
			t.Fatalf("err=%v len=%d, want %v and no trace", err, g.Len(), c.want)
		}
		if err := g.Add(Txn{Seq: 1, Writes: []string{"ok"}}); err != nil {
			t.Fatalf("engine unusable after rejection: %v", err)
		}
	}
}
