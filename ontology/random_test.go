package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// liveViews returns the set of reads compared after every step: each
// distinct held snapshot plus Latest.
func liveViews(m *naiveModel) []uint64 {
	seqs := m.heldSeqs()
	views := make([]uint64, 0, len(seqs)+1)
	for _, s := range seqs {
		views = append(views, uint64(s))
	}
	views = append(views, Latest)
	return views
}

func compareViews(t *testing.T, c *Compactor, m *naiveModel, phase string) {
	t.Helper()
	for _, k := range []string{"k0", "k1", "k2", "k3"} {
		for _, s := range liveViews(m) {
			gv, gok, gerr := c.Get(k, s)
			if gerr != nil {
				t.Fatalf("[%s] real Get(%q,%d) error: %v", phase, k, s, gerr)
			}
			nv, nok := m.get(k, s)
			if gok != nok || (gok && gv != nv) {
				t.Fatalf("[%s] Get(%q,%d): real=(%d,%v) naive=(%d,%v)\ninput=%s\noutput=%s",
					phase, k, s, gv, gok, nv, nok,
					recsString(m.recs), recsString(c.Records()))
			}
		}
	}
}

// TestRandomAgainstNaive replays 2000 random operation sequences on both
// the implementation and the literal naive model. After every operation
// and every compaction it compares Get for each held snapshot and Latest,
// and after each Compact it compares the exact record set and Stats.
func TestRandomAgainstNaive(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(20261002))

	for tc := 0; tc < cases; tc++ {
		ops := rng.Intn(40) + 1
		deeper := map[string]int64{}
		if rng.Intn(3) == 0 {
			for _, k := range []string{"k0", "k1", "k4"} {
				if rng.Intn(2) == 0 {
					deeper[k] = int64(rng.Intn(21)) - 10
				}
			}
		}
		c := New(deeper)
		m := newNaive(deeper)

		// snapshot sequence values still held, with reference counts.
		type hold struct {
			seq int64
			n   int
		}
		var active []*hold

		doCompact := func(tag string) {
			before := c.stripeProbes
			st := c.Compact()
			used := c.stripeProbes - before
			budget := probeBudget(st.In, len(m.heldSeqs()))
			if used > budget {
				t.Fatalf("case=%d %s stripe probes %d > budget %d (In=%d, |S|=%d)",
					tc, tag, used, budget, st.In, len(m.heldSeqs()))
			}
			nrecs, nst := m.naiveCompact()
			if st != nst {
				t.Fatalf("case=%d %s Stats: real=%+v naive=%+v\ninput=%s",
					tc, tag, st, nst, recsString(m.recs))
			}
			checkConservation(t, st)
			crecs := c.Records()
			if len(crecs) != len(nrecs) {
				t.Fatalf("case=%d %s len: real=%s\nnaive=%s\nstats=%+v",
					tc, tag, recsString(crecs), recsString(nrecs), st)
			}
			for i := range crecs {
				if crecs[i] != nrecs[i] {
					t.Fatalf("case=%d %s record[%d]: real=%+v naive=%+v\nreal out=%s\nnaive out=%s",
						tc, tag, i, crecs[i], nrecs[i],
						recsString(crecs), recsString(nrecs))
				}
			}
			compareViews(t, c, m, fmt.Sprintf("case=%d %s post-compact", tc, tag))
		}

		for op := 0; op < ops; op++ {
			k := []string{"k0", "k1", "k2", "k3"}[rng.Intn(4)]
			switch rng.Intn(7) {
			case 0, 1:
				v := int64(rng.Intn(21)) - 10
				r1, e1 := c.Put(k, v)
				if e1 != nil {
					t.Fatalf("case=%d put: %v", tc, e1)
				}
				r2 := m.add(k, KindPut, v)
				if r1 != r2 {
					t.Fatalf("case=%d put seq real=%d naive=%d", tc, r1, r2)
				}
			case 2, 3:
				d := int64(rng.Intn(21)) - 10
				r1, e1 := c.Merge(k, d)
				if e1 != nil {
					t.Fatalf("case=%d merge: %v", tc, e1)
				}
				r2 := m.add(k, KindMerge, d)
				if r1 != r2 {
					t.Fatalf("case=%d merge seq real=%d naive=%d", tc, r1, r2)
				}
			case 4:
				r1, e1 := c.Delete(k)
				if e1 != nil {
					t.Fatalf("case=%d delete: %v", tc, e1)
				}
				r2 := m.add(k, KindDelete, 0)
				if r1 != r2 {
					t.Fatalf("case=%d delete seq real=%d naive=%d", tc, r1, r2)
				}
			case 5:
				s1 := c.Snapshot()
				s2 := m.snapshot()
				if s1 != s2 {
					t.Fatalf("case=%d snapshot real=%d naive=%d", tc, s1, s2)
				}
				var found *hold
				for _, h := range active {
					if h.seq == s1 {
						found = h
					}
				}
				if found == nil {
					active = append(active, &hold{seq: s1})
					found = active[len(active)-1]
				}
				found.n++
			case 6:
				if len(active) > 0 && rng.Intn(2) == 0 {
					h := active[rng.Intn(len(active))]
					e1 := c.Release(h.seq)
					if e1 != nil {
						t.Fatalf("case=%d release %d: %v", tc, h.seq, e1)
					}
					if !m.release(h.seq) {
						t.Fatalf("case=%d naive release %d failed", tc, h.seq)
					}
					h.n--
					if h.n == 0 {
						for i, x := range active {
							if x == h {
								active = append(active[:i], active[i+1:]...)
								break
							}
						}
					}
				}
			}
			compareViews(t, c, m, fmt.Sprintf("case=%d op=%d", tc, op))
			if rng.Intn(4) == 0 {
				doCompact(fmt.Sprintf("op=%d", op))
			}
		}
		doCompact("final")

		if tc < 10 || testing.Verbose() {
			t.Logf("case=%d ops=%d deeper=%v final=%s verdict=read/records/stats identical",
				tc, ops, deeper, recsString(c.Records()))
		}
	}
}
