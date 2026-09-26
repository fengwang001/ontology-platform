package repl

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/sm"
)

type snapshot struct{ n, committed, lastApplied, state, reads int }

func grab(r *Replica) snapshot {
	return snapshot{len(r.log), r.committed, r.lastApplied, r.state, r.reads}
}
func fold(base int, cs []sm.Cmd) int {
	for _, c := range cs {
		base = sm.Apply(c, base)
	}
	return base
}
func TestIndexInvariant(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		r := New()
		var log []sm.Cmd
		snapIdx, base := 0, 0
		rng := rand.New(rand.NewSource(seed))
		for step := 0; step < 150; step++ {
			switch rng.Intn(4) {
			case 0:
				c := sm.Add(rng.Intn(7) - 3)
				if rng.Intn(2) == 0 {
					c = sm.Mul(rng.Intn(5))
				}
				if e := r.Append(c); e != nil {
					t.Fatalf("seed=%d append: %v", seed, e)
				}
				log = append(log, c)
			case 1:
				i := rng.Intn(len(log)+3) - 1
				e := r.Commit(i)
				if (i < 0 || i > len(log)) && !errors.Is(e, ErrCommitOutOfRange) {
					t.Fatalf("seed=%d want ErrCommitOutOfRange got %v", seed, e)
				}
			case 2:
				r.Apply()
			case 3:
				i := rng.Intn(r.committed + 2)
				if rng.Intn(3) == 0 {
					i = -1
				}
				st := rng.Intn(21) - 10
				e := r.Restart(i, st)
				if i < 0 || i > r.committed {
					if !errors.Is(e, ErrSnapshotOutOfRange) {
						t.Fatalf("seed=%d want ErrSnapshotOutOfRange got %v", seed, e)
					}
					break
				}
				snapIdx, base = i, st
			}
			if !(0 <= r.lastApplied && r.lastApplied <= r.committed && r.committed <= len(r.log)) {
				t.Fatalf("seed=%d step=%d invariant broken la=%d c=%d len=%d", seed, step, r.lastApplied, r.committed, len(r.log))
			}
			r.Apply()
			if r.lastApplied != r.committed {
				t.Fatalf("seed=%d la=%d != c=%d after Apply", seed, r.lastApplied, r.committed)
			}
			if want := fold(base, log[snapIdx:r.committed]); r.state != want {
				t.Fatalf("seed=%d state=%d recompute=%d", seed, r.state, want)
			}
		}
	}
}
func TestRejectedOpsAtomic(t *testing.T) {
	if ErrCommitOutOfRange == ErrEmptyCommand || ErrCommitOutOfRange == ErrSnapshotOutOfRange ||
		ErrEmptyCommand == ErrSnapshotOutOfRange {
		t.Fatal("the three sentinel errors must be distinct")
	}
	cases := []struct {
		name string
		want error
		run  func(r *Replica) error
	}{
		{"append-empty", ErrEmptyCommand, func(r *Replica) error { return r.Append(sm.Cmd{}) }},
		{"append-unknown", ErrEmptyCommand, func(r *Replica) error { return r.Append(sm.Cmd{Kind: 99}) }},
		{"commit-high", ErrCommitOutOfRange, func(r *Replica) error { return r.Commit(9) }},
		{"commit-neg", ErrCommitOutOfRange, func(r *Replica) error { return r.Commit(-1) }},
		{"restart-high", ErrSnapshotOutOfRange, func(r *Replica) error { return r.Restart(9, 0) }},
		{"restart-neg", ErrSnapshotOutOfRange, func(r *Replica) error { return r.Restart(-1, 0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if e := r.Append(sm.Add(5)); e != nil {
				t.Fatal(e)
			}
			if e := r.Commit(1); e != nil {
				t.Fatal(e)
			}
			r.Apply()
			before := grab(r)
			if e := tc.run(r); !errors.Is(e, tc.want) {
				t.Fatalf("got %v want %v", e, tc.want)
			}
			if grab(r) != before {
				t.Fatalf("rejected op left a trace: %+v", grab(r))
			}
			if e := r.Append(sm.Mul(2)); e != nil {
				t.Fatal(e)
			}
			if e := r.Commit(2); e != nil {
				t.Fatal(e)
			}
			r.Apply()
			if r.state != 10 {
				t.Fatalf("replica unusable after rejection: state=%d", r.state)
			}
		})
	}
}
func TestIncrementalReadCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			if e := r.Append(sm.Add(1)); e != nil {
				t.Fatal(e)
			}
		}
		if e := r.Commit(m - 1); e != nil {
			t.Fatal(e)
		}
		r.Apply()
		if r.lastApplied != m-1 {
			t.Fatalf("m=%d la=%d want %d", m, r.lastApplied, m-1)
		}
		if e := r.Commit(m); e != nil {
			t.Fatal(e)
		}
		r.Apply()
		if r.reads != 1 {
			t.Fatalf("m=%d reads=%d want 1", m, r.reads)
		}
		r.Apply()
		if r.reads != 0 {
			t.Fatalf("m=%d idle reads=%d want 0", m, r.reads)
		}
	}
}
