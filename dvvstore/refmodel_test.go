package dvvstore

import "sort"

// Naive reference implementation written directly from the specification:
// plain maps, no synchronization, identical validation and reconciliation
// rules. The randomized test drives the real Store and this model with the
// same operation schedule and requires observable equality at every step.

type refSibling struct {
	dot   Dot
	value string
}

type refKeyState struct {
	siblings []refSibling
	seen     map[string]int64
}

type refStore struct {
	cap  int
	keys map[string]*refKeyState
}

func newRefStore(cap int) *refStore {
	return &refStore{cap: cap, keys: make(map[string]*refKeyState)}
}

func (r *refStore) put(key, id string, ctx map[string]int64, value string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if id == "" {
		return ErrEmptyNodeID
	}
	for node := range ctx {
		if node == "" {
			return ErrEmptyContextNode
		}
	}

	st := r.keys[key]
	for node, counter := range ctx {
		var seenCounter int64
		if st != nil {
			seenCounter = st.seen[node]
		}
		if counter > seenCounter {
			return ErrContextAhead
		}
	}

	var newCounter int64 = 1
	if st != nil {
		newCounter = st.seen[id] + 1
	}

	var kept []refSibling
	if st != nil {
		for _, sib := range st.siblings {
			if ctx[sib.dot.ID] < sib.dot.N {
				kept = append(kept, sib)
			}
		}
	}
	if len(kept)+1 > r.cap {
		return ErrCapExceeded
	}

	if st == nil {
		st = &refKeyState{seen: make(map[string]int64)}
		r.keys[key] = st
	}
	st.siblings = append(kept, refSibling{dot: Dot{ID: id, N: newCounter}, value: value})
	st.seen[id] = newCounter
	return nil
}

func cloneRefSeen(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for node, counter := range in {
		out[node] = counter
	}
	return out
}

func refHasDot(siblings []refSibling, dot Dot) bool {
	for _, sib := range siblings {
		if sib.dot == dot {
			return true
		}
	}
	return false
}

func (r *refStore) merge(other *refStore) {
	keys := make(map[string]bool, len(r.keys)+len(other.keys))
	for key := range r.keys {
		keys[key] = true
	}
	for key := range other.keys {
		keys[key] = true
	}
	for key := range keys {
		otherST := other.keys[key]
		st := r.keys[key]
		if st == nil {
			r.keys[key] = &refKeyState{
				siblings: append([]refSibling(nil), otherST.siblings...),
				seen:     cloneRefSeen(otherST.seen),
			}
			continue
		}
		if otherST == nil {
			otherST = &refKeyState{seen: map[string]int64{}}
		}

		merged := make(map[Dot]refSibling)
		for _, sib := range st.siblings {
			if refHasDot(otherST.siblings, sib.dot) || otherST.seen[sib.dot.ID] < sib.dot.N {
				merged[sib.dot] = sib
			}
		}
		for _, sib := range otherST.siblings {
			if refHasDot(st.siblings, sib.dot) || st.seen[sib.dot.ID] < sib.dot.N {
				if existing, ok := merged[sib.dot]; ok {
					if sib.value > existing.value {
						merged[sib.dot] = sib
					}
				} else {
					merged[sib.dot] = sib
				}
			}
		}

		combined := make([]refSibling, 0, len(merged))
		for _, sib := range merged {
			combined = append(combined, sib)
		}
		sortRefSiblings(combined)
		st.siblings = combined

		for node, counter := range otherST.seen {
			if counter > st.seen[node] {
				st.seen[node] = counter
			}
		}
	}
}

// snapshot produces the same observable form as Store.Get.
func (r *refStore) snapshot(key string) Snapshot {
	out := Snapshot{Siblings: []Sibling{}, Context: map[string]int64{}}
	st := r.keys[key]
	if st == nil {
		return out
	}
	for _, sib := range st.siblings {
		out.Siblings = append(out.Siblings, Sibling{Dot: sib.dot, Value: sib.value})
	}
	for node, counter := range st.seen {
		out.Context[node] = counter
	}
	sortSnapshot(out.Siblings)
	return out
}

func sortRefSiblings(in []refSibling) {
	sort.Slice(in, func(i, j int) bool {
		if in[i].dot.ID != in[j].dot.ID {
			return in[i].dot.ID < in[j].dot.ID
		}
		return in[i].dot.N < in[j].dot.N
	})
}

func sortSnapshot(in []Sibling) {
	sortSiblings(in)
}

// allSnapshots compares the full key set of two stores.
func (r *refStore) keySet() map[string]bool {
	out := make(map[string]bool, len(r.keys))
	for key := range r.keys {
		out[key] = true
	}
	return out
}
