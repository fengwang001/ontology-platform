package cascade

import (
	"math/rand"
	"reflect"
	"testing"
)

// naiveResult is produced by an independent, deliberately slow and
// order-randomized reference implementation that keeps the whole graph and
// every keep-alive marker and simulates rules one link at a time.
type naiveResult struct {
	deleted map[string]bool
	removed map[string]bool
	err     *CascadeError
}

// naiveDelete models one request with random scheduling at every choice
// point. It mirrors the specified rules but shares no code with plan().
func naiveDelete(cfg *Config, snap snapshot, root string, rng *rand.Rand) naiveResult {
	if !snap.objects[root] {
		return naiveResult{err: &CascadeError{Code: ErrObjectNotFound, Detail: root}}
	}
	normal := map[string]bool{root: true}
	deleted := map[string]bool{root: true}
	removed := map[string]bool{}

	alive := map[string]bool{}
	for o := range snap.objects {
		alive[o] = true
	}
	links := append([]Link(nil), snap.links...)

	for {
		// Phase A: drain every currently enabled rule job in random order.
		// Orphan collection only runs once this queue is stable, otherwise
		// an object that is guaranteed to be cascade-deleted next could be
		// mis-collected as an orphan (its incoming rules would be skipped).
		type job struct {
			obj    string
			inRule bool
		}
		collectJobs := func() []job {
			seen := map[job]bool{}
			var jobs []job
			for _, l := range links {
				if removed[l.ID] || !alive[l.Src] || !alive[l.Dst] {
					continue
				}
				if _, ok := cfg.types[l.Type]; !ok {
					continue
				}
				if deleted[l.Src] {
					jb := job{l.Src, false}
					if !seen[jb] {
						seen[jb] = true
						jobs = append(jobs, jb)
					}
				}
				if deleted[l.Dst] && normal[l.Dst] {
					jb := job{l.Dst, true}
					if !seen[jb] {
						seen[jb] = true
						jobs = append(jobs, jb)
					}
				}
			}
			return jobs
		}
		jobs := collectJobs()
		for len(jobs) > 0 {
			j := jobs[rng.Intn(len(jobs))]
			for _, l := range links {
				if removed[l.ID] || !alive[l.Src] || !alive[l.Dst] {
					continue
				}
				if (!j.inRule && l.Src != j.obj) || (j.inRule && l.Dst != j.obj) {
					continue
				}
				def, ok := cfg.types[l.Type]
				if !ok {
					continue
				}
				rule := def.OutRule
				if j.inRule {
					rule = def.InRule
				}
				removed[l.ID] = true
				other := l.Dst
				if j.inRule {
					other = l.Src
				}
				if rule == RuleCascade && alive[other] && !deleted[other] {
					deleted[other] = true
					normal[other] = true
				}
				break
			}
			jobs = collectJobs()
		}

		// Random orphan sweep: any object whose keep-alive inbound links
		// all come from deleted sources, and which originally had such a
		// link from a deleted source, is collected.
		var cands []string
		hadKeeperFromDeleted := map[string]bool{}
		for _, l := range links {
			def, ok := cfg.types[l.Type]
			if !ok || !def.KeepAlive {
				continue
			}
			if deleted[l.Src] && alive[l.Dst] && !deleted[l.Dst] {
				hadKeeperFromDeleted[l.Dst] = true
			}
		}
		for cand := range hadKeeperFromDeleted {
			supported := false
			for _, l := range links {
				if removed[l.ID] || !alive[l.Src] {
					continue
				}
				def, ok := cfg.types[l.Type]
				if ok && def.KeepAlive && l.Dst == cand && !deleted[l.Src] {
					supported = true
				}
			}
			if !supported {
				cands = append(cands, cand)
			}
		}
		if len(cands) > 0 {
			pick := cands[rng.Intn(len(cands))]
			deleted[pick] = true
		} else {
			break
		}
	}

	for o := range deleted {
		alive[o] = false
	}
	for _, l := range links {
		if deleted[l.Src] || deleted[l.Dst] {
			removed[l.ID] = true
		}
	}

	var restrictLinks, undefined []Link
	for _, l := range links {
		if !deleted[l.Src] && !deleted[l.Dst] {
			continue
		}
		if def, ok := cfg.types[l.Type]; !ok {
			undefined = append(undefined, l)
		} else {
			if deleted[l.Src] && !deleted[l.Dst] && def.OutRule == RuleRestrict {
				restrictLinks = append(restrictLinks, l)
			}
			if normal[l.Dst] && !deleted[l.Src] && def.InRule == RuleRestrict {
				restrictLinks = append(restrictLinks, l)
			}
		}
	}
	sortLinks(restrictLinks)
	sortLinks(undefined)
	if len(restrictLinks) > 0 {
		return naiveResult{err: &CascadeError{Code: ErrRestricted, Detail: restrictLinks[0].ID}}
	}
	if len(undefined) > 0 {
		return naiveResult{err: &CascadeError{Code: ErrUndefinedLinkType, Detail: undefined[0].Type}}
	}
	return naiveResult{deleted: deleted, removed: removed}
}

func randomConfig(rng *rand.Rand) (map[string]LinkType, []string) {
	names := []string{"t0", "t1", "t2", "t3"}
	defs := map[string]LinkType{}
	for i, name := range names {
		// Occasionally omit a type so undefined errors are exercised.
		if rng.Intn(8) == 0 {
			continue
		}
		defs[name] = LinkType{
			OutRule:   Rule(rng.Intn(3)),
			InRule:    Rule(rng.Intn(3)),
			KeepAlive: i > 0 && rng.Intn(2) == 0,
		}
	}
	return defs, names
}

func randomGraph(rng *rand.Rand, types []string) (*MemoryStore, []string) {
	store := NewMemoryStore()
	n := 1 + rng.Intn(10)
	var objs []string
	for i := 0; i < n; i++ {
		objs = append(objs, "o"+itoa(i))
		store.AddObject(objs[i])
	}
	for i := 0; i < 2*n; i++ {
		a := objs[rng.Intn(n)]
		b := objs[rng.Intn(n)]
		name := types[rng.Intn(len(types))]
		store.links["e"+itoa(i)] = Link{ID: "e" + itoa(i), Type: name, Src: a, Dst: b}
	}
	return store, objs
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// Differential test over many random graphs; every graph is planned under
// several naive orderings and compared with the production planner.
func TestDifferentialAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for iter := 0; iter < 400; iter++ {
		defs, names := randomConfig(rng)
		cfg := NewConfig(defs)
		store, objs := randomGraph(rng, names)
		snap := store.snapshot()
		root := objs[rng.Intn(len(objs))]

		got, gotErr := plan(cfg, snap, root)
		var reference *naiveResult
		for order := 0; order < 4; order++ {
			r := naiveDelete(cfg, snap, root, rand.New(rand.NewSource(int64(iter*10+order+1))))
			if reference == nil {
				reference = &r
			} else {
				if (r.err == nil) != (reference.err == nil) ||
					(r.err != nil && r.err.Code != reference.err.Code) {
					t.Fatalf("iter %d naive orders disagree: %v vs %v", iter, r.err, reference.err)
				}
				if r.err == nil && (!reflect.DeepEqual(r.deleted, reference.deleted) ||
					!reflect.DeepEqual(r.removed, reference.removed)) {
					t.Fatalf("iter %d naive orders disagree on sets", iter)
				}
			}
		}
		if (gotErr == nil) != (reference.err == nil) ||
			(gotErr != nil && gotErr.Code != reference.err.Code) {
			t.Fatalf("iter %d root %s: engine %v naive %v", iter, root, gotErr, reference.err)
		}
		if gotErr == nil {
			if !reflect.DeepEqual(got.Deleted, reference.deleted) {
				t.Fatalf("iter %d deleted mismatch: %v vs %v", iter, got.Deleted, reference.deleted)
			}
			if !reflect.DeepEqual(got.RemovedLinks, reference.removed) {
				t.Fatalf("iter %d removed mismatch", iter)
			}
		}
	}

	// Missing root is independent of the graph; check it once separately.
	if _, err := plan(NewConfig(map[string]LinkType{}), NewMemoryStore().snapshot(), "x"); err.Code != ErrObjectNotFound {
		t.Fatalf("missing root: %v", err)
	}
}
