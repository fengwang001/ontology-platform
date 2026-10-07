package gc

import "sort"

// store keeps the object graph and the derived indexes that make every
// operation cost only as much as the objects and references it touches:
//
//   - objs:       id -> object
//   - dependents: owner id -> set of dependent ids (reverse edge index)
//   - blocking:   owner id -> number of *live* (non-deleting) dependents
//     whose reference to this owner has Block=true. This is the gate for
//     foreground deletion. The counter is adjusted at the exact moments a
//     reference appears/disappears or a dependent enters deletion.
//
// Invariants maintained here:
//   - every owner reference points to an existing object;
//   - dependents[x] contains exactly the objects holding a reference to x;
//   - blocking[x] counts exactly the live blocking references to x.
type store struct {
	objs       map[string]*Object
	dependents map[string]map[string]struct{}
	blocking   map[string]int
}

func newStore() *store {
	return &store{
		objs:       make(map[string]*Object),
		dependents: make(map[string]map[string]struct{}),
		blocking:   make(map[string]int),
	}
}

func (s *store) addEdge(dependentID string, ref OwnerReference) {
	set := s.dependents[ref.ID]
	if set == nil {
		set = make(map[string]struct{})
		s.dependents[ref.ID] = set
	}
	set[dependentID] = struct{}{}
}

func (s *store) removeEdge(dependentID string, ref OwnerReference) {
	if set := s.dependents[ref.ID]; set != nil {
		delete(set, dependentID)
		if len(set) == 0 {
			delete(s.dependents, ref.ID)
		}
	}
}

// insert adds a new object and registers its outgoing owner references.
// A freshly created object is alive, so its blocking references count.
func (s *store) insert(o *Object) {
	s.objs[o.ID] = o
	for _, ref := range o.Owners {
		s.addEdge(o.ID, ref)
		if ref.Block {
			s.blocking[ref.ID]++
		}
	}
}

// detach removes the reference dep -> ownerID from dep and from the indexes.
// If dep is still alive its blocking references keep counting, so the
// counter is adjusted here; a deleting dep was already discounted when it
// entered deletion.
func (s *store) detach(dep *Object, ownerID string) {
	for i, ref := range dep.Owners {
		if ref.ID == ownerID {
			dep.Owners = append(dep.Owners[:i], dep.Owners[i+1:]...)
			s.removeEdge(dep.ID, ref)
			if ref.Block && !dep.Deleting {
				s.blocking[ownerID]--
			}
			return
		}
	}
}

// replaceOwners swaps the owner set of o, keeping the indexes consistent.
// o must be alive (deleting objects may not change owners), so every
// blocking reference added or removed moves the counter. It returns the
// IDs of owners whose blocking count decreased, because those objects may
// have become removable.
func (s *store) replaceOwners(o *Object, refs []OwnerReference) []string {
	newByID := make(map[string]OwnerReference, len(refs))
	for _, ref := range refs {
		newByID[ref.ID] = ref
	}
	var unblocked []string
	kept := make([]OwnerReference, 0, len(refs))
	keptSet := make(map[string]struct{}, len(refs))
	for _, ref := range o.Owners {
		newRef, ok := newByID[ref.ID]
		if !ok {
			s.removeEdge(o.ID, ref)
			if ref.Block {
				s.blocking[ref.ID]--
				unblocked = append(unblocked, ref.ID)
			}
			continue
		}
		if newRef.Block != ref.Block {
			if newRef.Block {
				s.blocking[ref.ID]++
			} else {
				s.blocking[ref.ID]--
				unblocked = append(unblocked, ref.ID)
			}
		}
		kept = append(kept, newRef)
		keptSet[ref.ID] = struct{}{}
	}
	for _, ref := range refs {
		if _, ok := keptSet[ref.ID]; !ok {
			s.addEdge(o.ID, ref)
			if ref.Block {
				s.blocking[ref.ID]++
			}
			kept = append(kept, ref)
		}
	}
	o.Owners = kept
	return unblocked
}

// dependentIDs returns the sorted IDs of the current dependents of id.
func (s *store) dependentIDs(id string) []string {
	set := s.dependents[id]
	ids := make([]string, 0, len(set))
	for depID := range set {
		ids = append(ids, depID)
	}
	sort.Strings(ids)
	return ids
}

// wouldCycle reports whether giving the object id exactly the owner
// references newOwners would close a cycle. It walks the owner graph
// upwards from id (using the proposed references for id itself) and checks
// whether any proposed new owner is reachable. Only ancestors of id are
// visited, so the cost never depends on unrelated objects.
func (s *store) wouldCycle(id string, newOwners []OwnerReference) bool {
	targets := make(map[string]struct{}, len(newOwners))
	for _, ref := range newOwners {
		targets[ref.ID] = struct{}{}
	}
	visited := map[string]struct{}{id: {}}
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		var refs []OwnerReference
		if cur == id {
			refs = newOwners
		} else if o := s.objs[cur]; o != nil {
			refs = o.Owners
		}
		for _, ref := range refs {
			if _, ok := targets[ref.ID]; ok {
				return true
			}
			if _, ok := visited[ref.ID]; !ok {
				visited[ref.ID] = struct{}{}
				stack = append(stack, ref.ID)
			}
		}
	}
	return false
}
