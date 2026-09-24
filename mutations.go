package ontology

import "sort"

// Add places vnodes virtual nodes for id on the ring.
//
// It returns ErrInvalidVnodes when vnodes <= 0 and ErrNodeExists when id is
// already present; in both cases the ring is left untouched.
func (r *Ring) Add(id string, vnodes int) error {
	if vnodes <= 0 {
		return ErrInvalidVnodes
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.nodes[id]; ok {
		return ErrNodeExists
	}

	for i := 0; i < vnodes; i++ {
		r.ring = append(r.ring, vnode{
			pos:  vnodePosition(id, i),
			node: id,
		})
	}
	// Sort by position, then by node ID. The secondary key resolves hash
	// collisions deterministically and independently of insertion order:
	// the lexicographically smallest node always wins a tied position.
	sort.Slice(r.ring, func(i, j int) bool {
		if r.ring[i].pos != r.ring[j].pos {
			return r.ring[i].pos < r.ring[j].pos
		}
		return r.ring[i].node < r.ring[j].node
	})
	r.nodes[id] = vnodes
	return nil
}

// Remove deletes every vnode owned by id. It returns ErrNodeNotFound when id
// is absent and leaves the ring untouched in that case.
func (r *Ring) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	count, ok := r.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}

	kept := r.ring[:0]
	removed := 0
	for _, vn := range r.ring {
		if vn.node == id {
			removed++
			continue
		}
		kept = append(kept, vn)
	}
	// Defence in depth: every owned vnode must have been present.
	if removed != count {
		panic("ontology: ring bookkeeping mismatch")
	}
	r.ring = kept
	delete(r.nodes, id)
	return nil
}

// Len returns the number of real nodes currently on the ring.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}
