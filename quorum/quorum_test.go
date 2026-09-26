package quorum

import (
	"math/rand"
	"testing"
)

// naiveChosen rescans every acceptor and recomputes the majority value.
func naiveChosen(c *Cluster) (int, bool) {
	votes := map[int]int{}
	for i := 0; i < c.N(); i++ {
		_, ap, av := c.SnapshotAt(i)
		if ap > 0 {
			votes[av]++
		}
	}
	for v, k := range votes {
		if k >= c.N()/2+1 {
			return v, true
		}
	}
	return 0, false
}

// TestChosenMatchesNaiveRecompute drives random operation sequences (random
// arrival order of prepares and accepts) and checks after every step that the
// incrementally maintained answer equals a full naive rescan.
func TestChosenMatchesNaiveRecompute(t *testing.T) {
	sizes := []int{3, 5, 7, 101, 999}
	for _, m := range sizes {
		rng := rand.New(rand.NewSource(int64(m)))
		c := New(m)
		steps := 2000
		if m > 100 {
			steps = 400
		}
		for s := 0; s < steps; s++ {
			acc := rng.Intn(m)
			n := 1 + rng.Intn(4)
			if rng.Intn(2) == 0 {
				c.Prepare(acc, n)
			}
			v := []int{10, 20, 30}[rng.Intn(3)]
			c.Accept(acc, n, v) // refusals are legal no-ops and must agree too

			gotV, gotOK := c.Chosen()
			wantV, wantOK := naiveChosen(c)
			if gotV != wantV || gotOK != wantOK {
				t.Fatalf("m=%d step=%d: Chosen=(%d,%v), naive=(%d,%v)",
					m, s, gotV, gotOK, wantV, wantOK)
			}
			// Chosen reads only the counts table: the acceptor-read counter
			// must stay exactly zero regardless of cluster size.
			if r := c.reads.Load(); r != 0 {
				t.Fatalf("m=%d step=%d: Chosen inspected %d acceptors", m, s, r)
			}
		}
	}
}

// TestChosenReadCountConstantInM: with every acceptor holding the same value,
// the internal read count must stay within a small constant independent of m.
func TestChosenReadCountConstantInM(t *testing.T) {
	for _, m := range []int{101, 1001, 4999, 9999} {
		if m%2 == 0 {
			t.Fatalf("test size must be odd: %d", m)
		}
		c := New(m)
		for i := 0; i < m; i++ {
			c.acceptors[i].Prepare(1)
			if !c.Accept(i, 1, 42) {
				t.Fatalf("m=%d: accept at %d refused", m, i)
			}
		}
		if v, ok := c.Chosen(); !ok || v != 42 || c.reads.Load() > 0 {
			t.Fatalf("m=%d: Chosen=(%d,%v) reads=%d, want 42,true,0", m, v, ok, c.reads.Load())
		}
	}
}

func TestPickValue(t *testing.T) {
	cases := []struct {
		name    string
		reports []Report
		own     int
		want    int
		reused  bool
	}{
		{"round3 reuses 10", []Report{{2, 10}, {2, 10}, {0, 0}}, 20, 10, true},
		{"highest accepted number wins", []Report{{3, 20}, {1, 10}}, 5, 20, true},
		{"tie broken by highest number", []Report{{1, 10}, {2, 30}}, 5, 30, true},
		{"nobody accepted: use own", []Report{{0, 0}, {0, 0}}, 7, 7, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, reused := PickValue(tc.reports, tc.own)
			if v != tc.want || reused != tc.reused {
				t.Fatalf("PickValue = (%d,%v), want (%d,%v)", v, reused, tc.want, tc.reused)
			}
		})
	}
}
