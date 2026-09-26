package repl

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/sm"
)

func TestInvariantRecompute(t *testing.T) {
	for _, tc := range [][2]int{{20, 1}, {100, 3}} {
		n, seed := tc[0], int64(tc[1])
		r, cmds := New(), randCmds(n, seed)
		for _, c := range cmds {
			_ = r.Append(c)
		}
		k := n * 2 / 3
		_ = r.Commit(k)
		r.Apply()
		base := r.State()
		if err := r.Restart(k, base); err != nil {
			t.Fatal(err)
		}
		_ = r.Commit(n)
		r.Apply()
		if got := r.State(); got != recompute(cmds[k:], n-k, base) {
			t.Fatalf("n=%d state=%d", n, got)
		}
	}
}
func TestIndexInvariant(t *testing.T) {
	r, rng := New(), rand.New(rand.NewSource(99))
	for i := 0; i < 300; i++ {
		_ = r.Append(sm.Command{Op: sm.Add, K: 1})
		switch i % 4 {
		case 0:
			_ = r.Commit(rng.Intn(len(r.log) + 1))
		case 1:
			r.Apply()
			if r.lastApplied != r.committed {
				t.Fatalf("after Apply %d!=%d", r.lastApplied, r.committed)
			}
		case 2:
			if r.committed > 0 {
				_ = r.Restart(rng.Intn(r.committed+1), r.state)
			}
		}
		if !(0 <= r.lastApplied && r.lastApplied <= r.committed && r.committed <= len(r.log)) {
			t.Fatalf("broken la=%d committed=%d len=%d", r.lastApplied, r.committed, len(r.log))
		}
	}
}
func TestBatchingConverges(t *testing.T) {
	for _, seed := range []int64{7, 77, 777} {
		cmds := randCmds(150, seed)
		a, b := New(), New()
		for _, c := range cmds {
			_ = a.Append(c)
			_ = b.Append(c)
		}
		rng := rand.New(rand.NewSource(seed))
		for c := 0; c < len(cmds); {
			c += 1 + rng.Intn(9)
			if c > len(cmds) {
				c = len(cmds)
			}
			_ = a.Commit(c)
			a.Apply()
		}
		_ = b.Commit(len(cmds))
		b.Apply()
		if a.State() != b.State() || a.State() != recompute(cmds, len(cmds), 0) {
			t.Fatalf("seed=%d batch=%d once=%d", seed, a.State(), b.State())
		}
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name string
		call func(*Replica) error
		want error
	}{
		{"commit high", func(r *Replica) error { return r.Commit(1) }, ErrCommitOutOfRange},
		{"commit neg", func(r *Replica) error { return r.Commit(-1) }, ErrCommitOutOfRange},
		{"append nil", func(r *Replica) error { return r.Append(sm.Command{}) }, ErrEmptyCommand},
		{"append unknown", func(r *Replica) error { return r.Append(sm.Command{Op: sm.Op(9)}) }, ErrEmptyCommand},
		{"restart high", func(r *Replica) error { return r.Restart(1, 0) }, ErrSnapOutOfRange},
		{"restart neg", func(r *Replica) error { return r.Restart(-1, 0) }, ErrSnapOutOfRange},
	}
	for _, tc := range cases {
		r := New()
		before := triple{r.Committed(), r.LastApplied(), r.State()}
		if err := tc.call(r); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v want %v", tc.name, err, tc.want)
		}
		if after := (triple{r.Committed(), r.LastApplied(), r.State()}); after != before || len(r.log) != 0 {
			t.Fatalf("%s: state changed", tc.name)
		}
		if err := r.Append(sm.Command{Op: sm.Add, K: 1}); err != nil {
			t.Fatalf("%s: unusable after rejection", tc.name)
		}
	}
	if errors.Is(ErrCommitOutOfRange, ErrEmptyCommand) || errors.Is(ErrEmptyCommand, ErrSnapOutOfRange) {
		t.Fatal("sentinel errors must be distinct")
	}
}
func TestIncrementalRead(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		fillAdds(r, m)
		_ = r.Commit(m - 1)
		r.Apply()
		_ = r.Commit(m)
		r.Apply()
		if r.readCount != 1 {
			t.Fatalf("m=%d readCount=%d want 1", m, r.readCount)
		}
	}
}
func TestConcurrentReaders(t *testing.T) {
	for _, n := range []int{4, 16, 64} {
		r := New()
		fillAdds(r, 50)
		_ = r.Commit(50)
		r.Apply()
		got := make(chan triple, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); got <- triple{r.State(), r.LastApplied(), r.Committed()} }()
		}
		wg.Wait()
		close(got)
		first := <-got
		for v := range got {
			if v != first {
				t.Fatalf("n=%d disagree %v vs %v", n, first, v)
			}
		}
	}
}
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
