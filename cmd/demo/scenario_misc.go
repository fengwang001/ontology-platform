package main

import (
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/audit"
	"ontology/change"
	"ontology/view"
)

// scenarioAudit maintains a small view in lockstep with a full-
// recomputation reference and returns whether they are identical.
func scenarioAudit() (bool, string) {
	v := openView("", view.Options{})
	defer v.Close()
	ref := audit.NewReference()
	changes := []change.Change{
		ins(1, "a", "g1", 0.1), ins(2, "b", "g1", 0.2),
		ins(3, "c", "g2", 1e16), ins(4, "d", "g1", -0.0),
	}
	for _, c := range changes {
		must(v.Submit(c))
		ref.Apply(c)
	}
	if mm := audit.Check(v, ref); mm != nil {
		return false, mm.Error()
	}
	return true, "(all groups bit-identical)"
}

// scenarioEmptyGroup deletes the sole member of a group and verifies that
// a subsequent lookup reports non-existence rather than a zero result.
func scenarioEmptyGroup() bool {
	v := openView("", view.Options{})
	defer v.Close()
	must(v.Submit(ins(1, "k", "ephemeral", 9)))
	must(v.Submit(del(2, "k", "ephemeral", 9)))
	_, ok := v.Lookup("ephemeral")
	return !ok && len(v.Groups()) == 0
}

// scenarioOutOfOrder sends a backward and a conflicting duplicate change;
// both must be rejected and counted, and the view stays unpolluted.
func scenarioOutOfOrder() int64 {
	v := openView("", view.Options{})
	defer v.Close()
	must(v.Submit(ins(5, "a", "g", 1)))
	_ = v.Submit(ins(4, "b", "g", 1)) // backward
	_ = v.Submit(ins(5, "c", "g", 2)) // same version, different payload
	return v.Stats().Rejected
}

// scenarioConcurrent fans concurrent writers out across groups and
// verifies every record is accounted for exactly once.
func scenarioConcurrent() bool {
	v := openView("", view.Options{})
	defer v.Close()
	const writers, per = 8, 500
	var ver atomic.Uint64
	var order sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				id := w*per + i
				group := fmt.Sprintf("g%d", id%4)
				order.Lock()
				err := v.Submit(ins(ver.Add(1), fmt.Sprintf("k%05d", id), group, 1))
				order.Unlock()
				if err != nil {
					panic(err)
				}
			}
		}(w)
	}
	wg.Wait()
	var total float64
	for _, name := range v.GroupNames() {
		g, ok := v.Lookup(name)
		if !ok {
			return false
		}
		total += g.Count
	}
	return int(total) == writers*per
}
