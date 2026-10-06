package fedalloc

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// TestDifferentialAgainstNaive runs hundreds of random small configurations and
// totals, comparing the production bulk allocator against the independent
// replica-by-replica naive model. Both must agree on targets and error kind.
func TestDifferentialAgainstNaive(t *testing.T) {
	l := newLogger(t)
	defer l.flush()

	rng := rand.New(rand.NewSource(20261006))
	iterations := 3000
	agreements := 0

	for it := 0; it < iterations; it++ {
		n := 1 + rng.Intn(6)
		cs := make([]Cluster, 0, n)
		ncs := make([]naiveCluster, 0, n)
		used := map[string]bool{}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("c%02d", i)
			used[name] = true
			weight := int64(rng.Intn(6)) // often zero
			minR := int64(rng.Intn(5))
			capVal := int64(rng.Intn(12))
			available := rng.Intn(5) != 0 // ~20% unavailable
			var maxR *int64
			if rng.Intn(2) == 0 {
				m := int64(rng.Intn(12))
				maxR = &m
			}
			load := int64(rng.Intn(10))
			c := Cluster{
				Name: name, Weight: weight, MinReplicas: minR, MaxReplicas: maxR,
				Capacity: capVal, Available: available, CurrentReplicas: load,
			}
			// skip registration-illegal configs (min>max) so the comparison
			// focuses on allocation-time semantics; keep min>cap conflicts.
			if maxR != nil && *maxR < minR {
				*maxR = minR + rng.Int63n(5)
				c.MaxReplicas = maxR
			}
			cs = append(cs, c)
			v := toView(c)
			ncs = append(ncs, naiveCluster{
				name: name, weight: weight, minReplicas: minR, cap: v.cap,
				available: available, currentReplicas: load,
			})
		}

		// randomize input order for the production registry
		shuf := append([]Cluster(nil), cs...)
		rng.Shuffle(len(shuf), func(i, j int) { shuf[i], shuf[j] = shuf[j], shuf[i] })
		r := NewRegistry()
		for _, c := range shuf {
			if err := r.Upsert(c); err != nil {
				t.Fatalf("unexpected upsert rejection: %v (%+v)", err, c)
			}
		}

		total := int64(rng.Intn(40)) // small so the naive per-unit model is cheap

		res, pErr := r.Allocate(total)
		nMap, nKind, nOK := naiveAllocate(ncs, total)

		caps := map[string]int64{}
		for _, nc := range ncs {
			caps[nc.name] = nc.cap
		}
		l.log("ITER %d n=%d total=%d config=%+v effectiveCaps=%v", it, n, total, cs, caps)

		if (pErr != nil) != (!nOK) {
			l.flush()
			t.Fatalf("iter %d total %d: production err=%v but naive ok=%v\nconfig=%+v",
				it, total, pErr, nOK, cs)
		}
		if pErr != nil {
			if pErr.Kind() != nKind {
				l.flush()
				t.Fatalf("iter %d total %d: kind mismatch prod=%d naive=%d\nconfig=%+v",
					it, total, pErr.Kind(), nKind, cs)
			}
			l.log("ITER %d both reject kind=%d (%v)", it, nKind, pErr)
			agreements++
			continue
		}

		pMap := targetsMap(res)
		// exact target equality
		names := make([]string, 0, len(nMap))
		for name := range nMap {
			names = append(names, name)
		}
		sort.Strings(names)
		sum := int64(0)
		for _, name := range names {
			sum += pMap[name]
			if pMap[name] != nMap[name] {
				l.flush()
				t.Fatalf("iter %d total %d mismatch on %s: prod=%d naive=%d\nconfig=%+v\nprod=%v\nnaive=%v",
					it, total, name, pMap[name], nMap[name], cs, pMap, nMap)
			}
		}
		if sum != total {
			l.flush()
			t.Fatalf("iter %d sum %d != total %d", it, sum, total)
		}

		// plan invariant: migration = sum of shrinkages; omitted when equal
		mig := int64(0)
		loadByName := map[string]int64{}
		for _, c := range cs {
			loadByName[c.Name] = c.CurrentReplicas
		}
		planned := map[string]bool{}
		for _, ch := range res.Plan {
			planned[ch.Name] = true
			if ch.Target != pMap[ch.Name] || ch.Current != loadByName[ch.Name] ||
				ch.Delta != ch.Target-ch.Current {
				l.flush()
				t.Fatalf("iter %d malformed change: %+v", it, ch)
			}
			if ch.Current > ch.Target {
				mig += ch.Current - ch.Target
			}
		}
		for name, tg := range pMap {
			if tg == loadByName[name] && planned[name] {
				l.flush()
				t.Fatalf("iter %d unchanged cluster %s present in plan", it, name)
			}
		}
		if mig != res.TotalMigration {
			l.flush()
			t.Fatalf("iter %d migration %d != computed %d", it, res.TotalMigration, mig)
		}

		l.log("ITER %d agree targets=%v migration=%d", it, pMap, res.TotalMigration)
		agreements++
	}

	l.check(agreements == iterations, "all %d random iterations match the naive model", agreements)
}
