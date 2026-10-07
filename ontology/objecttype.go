package ontology

import "sync"

// ObjectType is a node of the (single-inheritance) type hierarchy.
type ObjectType struct {
	ID     TypeID
	Parent *ObjectType
}

// chain returns [self, parent, grandparent, ...]; the type hierarchy is
// immutable in this design, so traversal reads no registry state.
func (t *ObjectType) chain() []*ObjectType {
	var out []*ObjectType
	for cur := t; cur != nil; cur = cur.Parent {
		out = append(out, cur)
	}
	return out
}

// Instance is an object whose current binding to a concrete type is fixed for
// the duration of a dispatch resolution.
type Instance struct {
	ID      string
	Type    *ObjectType
	revoked bool

	// mu guards revoked. It is intentionally separate from Registry state:
	// revocation is object lifecycle state, registration is dispatch state.
	mu sync.RWMutex
}

// Revoke marks the instance revoked; safe for concurrent use.
func (i *Instance) Revoke() {
	i.mu.Lock()
	i.revoked = true
	i.mu.Unlock()
}

// Revoked reports current revocation status.
func (i *Instance) Revoked() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.revoked
}
