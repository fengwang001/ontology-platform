package solve

import (
	"sort"

	"ontology/rng"
	"ontology/ver"
)

func (st *search) candidates(pkg string) []ver.Version {
	all, ok := st.g.Versions(pkg)
	if !ok {
		return nil
	}
	out := make([]ver.Version, 0, len(all))
	for _, v := range all {
		ok := true
		for _, e := range st.entries[pkg] {
			if !e.Range.Contains(v) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, v)
		}
	}
	return out
}

// firstRejection 返回最低候选被排除时命中的最小互斥条目对（确定性）。
func (st *search) firstRejection(pkg string) [2]rng.Entry {
	all, _ := st.g.Versions(pkg)
	es := st.entries[pkg]
	for _, v := range all {
		for _, e := range es {
			if e.Range.Contains(v) {
				continue
			}
			for _, f := range es {
				if originLess(f.Origin, e.Origin) && f.Range.Intersect(e.Range).Empty() {
					return [2]rng.Entry{f, e}
				}
			}
			return [2]rng.Entry{{}, e}
		}
	}
	return [2]rng.Entry{}
}

func incompatiblePairs(es []rng.Entry) [][2]rng.Entry {
	var pairs [][2]rng.Entry
	for i := 0; i < len(es); i++ {
		for j := i + 1; j < len(es); j++ {
			if es[i].Range.Intersect(es[j].Range).Empty() {
				pairs = append(pairs, [2]rng.Entry{es[i], es[j]})
			}
		}
	}
	sort.Slice(pairs, func(a, b int) bool {
		x, y := pairs[a], pairs[b]
		if !originEqual(x[0].Origin, y[0].Origin) {
			return originLess(x[0].Origin, y[0].Origin)
		}
		return originLess(x[1].Origin, y[1].Origin)
	})
	return pairs
}

func originEqual(a, b rng.Origin) bool { return !originLess(a, b) && !originLess(b, a) }

func originLess(a, b rng.Origin) bool {
	ra, rb := a.Pkg == "", b.Pkg == ""
	if ra != rb {
		return ra
	}
	if a.Pkg != b.Pkg {
		return a.Pkg < b.Pkg
	}
	if c := a.V.Compare(b.V); c != 0 {
		return c < 0
	}
	return a.Target < b.Target
}
