package hashagg

import (
	"sort"
	"sync"

	"ontology/acc"
	"ontology/hashpart"
)

// Finish 并发回读各分区全部溢出段，与内存余量确定性合并，输出按键排序。
func (a *Agg) Finish() ([]acc.Group, error) {
	if a.failed != nil {
		return nil, a.failed
	}
	spilled := make([]map[string]acc.State, a.numParts)
	readCounts := make([]int64, a.numParts)
	errs := make([]error, a.numParts)
	var wg sync.WaitGroup
	for p := 0; p < a.numParts; p++ {
		segs := a.spiller.Segments(p)
		if len(segs) == 0 {
			continue
		}
		spilled[p] = make(map[string]acc.State)
		wg.Add(1)
		go func(p int, segs []string) {
			defer wg.Done()
			if a.readDelay != nil {
				a.readDelay(p)
			}
			for _, path := range segs {
				f, err := hashpart.ReadFile(path)
				if err != nil {
					errs[p] = err
					return
				}
				for _, pl := range f.Payloads {
					key, st, err := acc.Decode(pl)
					if err != nil {
						errs[p] = err
						return
					}
					cur := spilled[p][key]
					cur.Merge(st)
					spilled[p][key] = cur
					readCounts[p]++
				}
			}
		}(p, segs)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			a.failed = err
			a.spiller.Cleanup()
			return nil, err
		}
}
	merged := make(map[string]acc.State)
	for p := 0; p < a.numParts; p++ {
		for k, st := range spilled[p] {
			merged[k] = st
		}
		for k, st := range a.table[p] {
			cur := merged[k]
			cur.Merge(*st)
			merged[k] = cur
		}
		a.rowsRead += readCounts[p]
		a.rowOps += readCounts[p]
	}
	groups := make([]acc.Group, 0, len(merged))
	for k, st := range merged {
		groups = append(groups, acc.Group{Key: k, State: st})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key })
	return groups, nil
}
