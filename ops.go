package ontology

import "sort"

// Connected reports whether a and b belong to the same class. It returns
// ErrUnknownElement if either ID is unknown, so "unknown" is never
// conflated with a plain false ("not connected").
func (ds *DisjointSet) Connected(a, b string) (bool, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.initLocked()
	if _, ok := ds.parent[a]; !ok {
		return false, ErrUnknownElement
	}
	if _, ok := ds.parent[b]; !ok {
		return false, ErrUnknownElement
	}
	ra, _ := ds.findRootLocked(a)
	rb, _ := ds.findRootLocked(b)
	return ra == rb, nil
}

// ClassCount returns the current number of equivalence classes.
func (ds *DisjointSet) ClassCount() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.count
}

// Classes exports all equivalence classes. Members within a class are
// sorted lexicographically, and classes are ordered by their
// representative (the smallest member). The output is fully
// deterministic: it depends neither on union order nor on internal map
// iteration order.
func (ds *DisjointSet) Classes() [][]string {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.initLocked()
	groups := make(map[string][]string)
	for id := range ds.parent {
		root, _ := ds.findRootLocked(id)
		groups[root] = append(groups[root], id)
	}
	out := make([][]string, 0, len(groups))
	for _, members := range groups {
		sort.Strings(members)
		out = append(out, members)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
