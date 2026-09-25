package check

import (
	"ontology/bank"
	"ontology/vec"
)

func Safe(avail vec.V, max, alloc map[int]vec.V) bool {
	pids := make([]int, 0, len(max))
	for p := range max {
		pids = append(pids, p)
	}
	return search(append(vec.V(nil), avail...), max, alloc, pids, 0)
}

func search(work vec.V, max, alloc map[int]vec.V, pids []int, i int) bool {
	if i == len(pids) {
		return true
	}
	for j := i; j < len(pids); j++ {
		pids[i], pids[j] = pids[j], pids[i]
		need, _ := vec.Sub(max[pids[i]], alloc[pids[i]])
		ok := false
		if fits, _ := vec.LE(need, work); fits {
			next, _ := vec.Add(work, alloc[pids[i]])
			ok = search(next, max, alloc, pids, i+1)
		}
		pids[i], pids[j] = pids[j], pids[i]
		if ok {
			return true
		}
	}
	return false
}

func Consistent(b *bank.Bank, total vec.V) bool {
	avail, max, alloc := b.Snapshot()
	sum := make(vec.V, len(total))
	ok := Safe(avail, max, alloc)
	for p, a := range alloc {
		fits, _ := vec.LE(a, max[p])
		ok = ok && fits
		sum, _ = vec.Add(sum, a)
	}
	fits, _ := vec.LE(sum, total)
	return ok && fits
}
