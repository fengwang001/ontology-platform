package shadow

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type naiveEntry struct {
	v  any
	mv int
}

type naiveSim struct {
	desired, reported map[string]any
	mvD, mvR          map[string]int
	ver               int
	delta             map[string]naiveEntry
}

func newNaive() *naiveSim {
	return &naiveSim{
		desired: map[string]any{}, reported: map[string]any{},
		mvD: map[string]int{}, mvR: map[string]int{},
		delta: map[string]naiveEntry{},
	}
}

func naiveMerge(docs, mvMap map[string]any, mvInt map[string]int, patch map[string]any, ver int, log *strings.Builder) bool {
	old := cloneFlat(docs)
	apply(docs, patch, nil)
	changed := false
	for p, v := range docs {
		if ov, ok := old[p]; !ok || !equalV(ov, v) {
			mvInt[p] = ver
			changed = true
			fmt.Fprintf(log, "      sim leaf %s set mv=%d\n", p, ver)
		}
	}
	for p := range old {
		if _, ok := docs[p]; !ok {
			delete(mvInt, p)
			changed = true
			fmt.Fprintf(log, "      sim leaf %s removed\n", p)
		}
	}
	_ = mvMap
	return changed
}

func apply(docs map[string]any, patch map[string]any, segs []string) {
	for k, v := range patch {
		p := append(append([]string{}, segs...), k)
		switch vv := v.(type) {
		case nil:
			deletePrefix(docs, p)
		case map[string]any:
			deleteLeaf(docs, p)
			apply(docs, vv, p)
		default:
			deletePrefix(docs, p)
			docs[strings.Join(p, ".")] = vv
		}
	}
}

func deleteLeaf(docs map[string]any, segs []string) {
	delete(docs, strings.Join(segs, "."))
}

func deletePrefix(docs map[string]any, segs []string) {
	pre := strings.Join(segs, ".")
	for p := range docs {
		if p == pre || strings.HasPrefix(p, pre+".") {
			delete(docs, p)
		}
	}
}

func equalV(a, b any) bool {
	return fmt.Sprintf("%T:%v", a, a) == fmt.Sprintf("%T:%v", b, b)
}

func cloneFlat(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneInt(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func naiveDelta(d, r map[string]any, mvD map[string]int) map[string]naiveEntry {
	out := map[string]naiveEntry{}
	for p, v := range d {
		rv, ok := r[p]
		if !ok || !equalV(rv, v) {
			out[p] = naiveEntry{v: v, mv: mvD[p]}
		}
	}
	return out
}

func mapToEntries(m map[string]naiveEntry) []DeltaEntry {
	out := make([]DeltaEntry, 0, len(m))
	for p, e := range m {
		out = append(out, DeltaEntry{Path: p, Value: e.v, Mv: e.mv})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func cloneNaive(m map[string]naiveEntry) map[string]naiveEntry {
	out := make(map[string]naiveEntry, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type dc struct {
	up map[string]naiveEntry
	rm []string
}

func diffDelta(old, cur map[string]naiveEntry) dc {
	c := dc{up: map[string]naiveEntry{}}
	for p, e := range cur {
		if oe, ok := old[p]; !ok || oe != e {
			c.up[p] = e
		}
	}
	for p := range old {
		if _, ok := cur[p]; !ok {
			c.rm = append(c.rm, p)
		}
	}
	sort.Strings(c.rm)
	return c
}

func sameUpsert(a, b []DeltaEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].Mv != b[i].Mv || !equalV(a[i].Value, b[i].Value) {
			return false
		}
	}
	return true
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func fmtUpsert(e []DeltaEntry) string {
	parts := make([]string, 0, len(e))
	for _, x := range e {
		parts = append(parts, fmt.Sprintf("%s=%T:%v(mv%d)", x.Path, x.Value, x.Value, x.Mv))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type randGen struct {
	rng    *rand.Rand
	keys   []string
	patchN int
}

func (g *randGen) seg() string { return g.keys[g.rng.Intn(len(g.keys))] }

func (g *randGen) leaf() any {
	switch g.rng.Intn(3) {
	case 0:
		return int64(g.rng.Intn(5))
	case 1:
		return []string{"a", "b", "1", "x"}[g.rng.Intn(4)]
	default:
		return g.rng.Intn(2) == 0
	}
}

func (g *randGen) patch() map[string]any {
	n := 1 + g.rng.Intn(g.patchN)
	root := map[string]any{}
	for i := 0; i < n; i++ {
		segs, v := g.entry()
		cur := root
		for _, seg := range segs[:len(segs)-1] {
			nx, ok := cur[seg].(map[string]any)
			if !ok {
				nx = map[string]any{}
				cur[seg] = nx
			}
			cur = nx
		}
		last := segs[len(segs)-1]
		// Never extend an existing value into an illegal 5th segment:
		// if the slot is already occupied by a leaf, replace the whole entry.
		cur[last] = v
	}
	return root
}

// entry builds one patch entry (path + value) that is legal by shape:
// every leaf/null is at most 4 key segments from the root.
func (g *randGen) entry() ([]string, any) {
	depth := 1 + g.rng.Intn(4)
	segs := make([]string, depth)
	for j := range segs {
		segs[j] = g.seg()
	}
	r := g.rng.Float64()
	switch {
	case r < 0.2:
		return segs, nil
	case r < 0.35:
		return segs, map[string]any{}
	case r < 0.5:
		if depth >= 4 {
			return segs, map[string]any{} // non-empty would need a 5th segment
		}
		sub := map[string]any{}
		subLeaves := 1 + g.rng.Intn(4-depth)
		for j := 0; j < subLeaves; j++ {
			sub[g.seg()] = g.leaf()
		}
		return segs, sub
	default:
		return segs, g.leaf()
	}
}

func TestRandomSequences(t *testing.T) {
	const runs = 1500
	var accepted, nochange, rejected int
	for run := 0; run < runs; run++ {
		seed := int64(1000 + run)
		rng := rand.New(rand.NewSource(seed))
		L := 1 + rng.Intn(12)
		s := NewService(L)
		sim := newNaive()
		dev := "dev"
		if err := s.Create(dev); err != nil {
			t.Fatalf("seed %d create: %v", seed, err)
		}
		g := &randGen{rng: rng, keys: []string{"a", "b", "c", "m", "d", "x", "y"}, patchN: 4}
		steps := 3 + rng.Intn(25)
		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d L=%d steps=%d\n", seed, L, steps)
		for st := 0; st < steps; st++ {
			side := "desired"
			if rng.Intn(2) == 0 {
				side = "reported"
			}
			p := g.patch()
			expect := 0
			if rng.Intn(3) == 0 {
				expect = sim.ver
			} else if rng.Intn(5) == 0 {
				expect = sim.ver + 1 + rng.Intn(3)
			}
			fmt.Fprintf(&log, " step %d %s expect=%d patch=%v\n", st, side, expect, p)

			docs, mvInt := sim.desired, sim.mvD
			if side == "reported" {
				docs, mvInt = sim.reported, sim.mvR
			}
			oldDelta := cloneNaive(sim.delta)

			var res *Result
			var gerr error
			if side == "desired" {
				res, gerr = s.UpdateDesired(dev, p, expect)
			} else {
				res, gerr = s.UpdateReported(dev, p, expect)
			}

			if expect != 0 && expect != sim.ver {
				if gerr == nil || !errors.Is(gerr, ErrVersion) {
					t.Fatalf("seed %d step %d: want ErrVersion got %v\n%s", seed, st, gerr, log.String())
				}
				fmt.Fprintf(&log, "   -> rejected ErrVersion (state unchanged)\n")
				rejected++
				continue
			}
			candDocs := cloneFlat(docs)
			candMv := cloneInt(mvInt)
			newVer := sim.ver + 1
			var mlog strings.Builder
			changed := naiveMerge(candDocs, nil, candMv, p, newVer, &mlog)
			if changed && len(candDocs) > L {
				if gerr == nil || !errors.Is(gerr, ErrTooLarge) {
					t.Fatalf("seed %d step %d: want ErrTooLarge got %v (leaves=%d)\n%s",
						seed, st, gerr, len(candDocs), log.String())
				}
				fmt.Fprintf(&log, "   -> rejected ErrTooLarge leaves=%d>L=%d\n", len(candDocs), L)
				rejected++
				continue
			}
			if gerr != nil {
				t.Fatalf("seed %d step %d: unexpected error %v\n%s", seed, st, gerr, log.String())
			}
			if !changed {
				if res.Ver != sim.ver {
					t.Fatalf("seed %d step %d: no-change ver=%d want %d\n%s",
						seed, st, res.Ver, sim.ver, log.String())
				}
				if len(res.Change.Upsert) != 0 || len(res.Change.Remove) != 0 {
					t.Fatalf("seed %d step %d: no-change produced delta change\n%s", seed, st, log.String())
				}
				fmt.Fprintf(&log, "   -> NoChange ver=%d\n", sim.ver)
				nochange++
				continue
			}
			docs2, mv2 := sim.desired, sim.mvD
			if side == "reported" {
				docs2, mv2 = sim.reported, sim.mvR
			}
			naiveMerge(docs2, nil, mv2, p, newVer, &strings.Builder{})
			sim.ver = newVer
			accepted++
			sim.delta = naiveDelta(sim.desired, sim.reported, sim.mvD)
			exp := diffDelta(oldDelta, sim.delta)
			fmt.Fprintf(&log, "%s", mlog.String())
			fmt.Fprintf(&log, "   -> accepted ver=%d upsert=%s remove=%v (judged by full recompute)\n",
				newVer, fmtUpsert(mapToEntries(exp.up)), exp.rm)
			if res.Ver != sim.ver {
				t.Fatalf("seed %d step %d ver mismatch %d!=%d\n%s", seed, st, res.Ver, sim.ver, log.String())
			}
			if !sameUpsert(res.Change.Upsert, mapToEntries(exp.up)) {
				t.Fatalf("seed %d step %d upsert mismatch:\n got %s\nwant %s\n%s",
					seed, st, fmtUpsert(res.Change.Upsert), fmtUpsert(mapToEntries(exp.up)), log.String())
			}
			if !eqStrings(res.Change.Remove, exp.rm) {
				t.Fatalf("seed %d step %d remove mismatch:\n got %v\nwant %v\n%s",
					seed, st, res.Change.Remove, exp.rm, log.String())
			}
		}
		for p, m := range sim.mvD {
			if m > sim.ver {
				t.Fatalf("seed %d mv %s=%d > ver %d", seed, p, m, sim.ver)
			}
			if got := mvOf(t, s, dev, "desired", p); got != m {
				t.Fatalf("seed %d mv desired %s: %d!=%d", seed, p, got, m)
			}
		}
		for p, m := range sim.mvR {
			if got := mvOf(t, s, dev, "reported", p); got != m {
				t.Fatalf("seed %d mv reported %s: %d!=%d", seed, p, got, m)
			}
		}
		gotDelta, err := s.GetDelta(dev)
		if err != nil {
			t.Fatalf("seed %d GetDelta: %v", seed, err)
		}
		if !sameUpsert(gotDelta, mapToEntries(sim.delta)) {
			t.Fatalf("seed %d final delta mismatch:\n got %s\nwant %s",
				seed, fmtUpsert(gotDelta), fmtUpsert(mapToEntries(sim.delta)))
		}
		if run%200 == 0 {
			t.Logf("progress run=%d accepted=%d nochange=%d rejected=%d", run, accepted, nochange, rejected)
		}
	}
	t.Logf("RANDOM SUMMARY runs=%d accepted=%d nochange=%d rejected=%d", runs, accepted, nochange, rejected)
}
