package ontology

import "fmt"

// runValidators executes each hook's Validate callback (if present) with
// immutable views of the current head snapshots of the target and linked
// instances. Validation failure leaves all state untouched and is itself
// recorded.
func (s *Store) runValidators(t TypeName, values map[Property]Value, cur *objState,
	rs resolvedScope, locked map[ObjectID]*objState, d *Decision) error {
	s.typeMu.RLock()
	hooks := s.types[t]
	s.typeMu.RUnlock()

	var targetView Snapshot
	if snap := cur.snapshotAt(cur.head); snap != nil {
		targetView = snap
	}
	linkedView := map[ObjectID]Snapshot{}
	for id, st := range locked {
		if id == d.Object {
			continue
		}
		if snap := st.snapshotAt(st.head); snap != nil {
			linkedView[id] = snap
		}
	}
	for _, h := range hooks {
		if h.Validate == nil {
			continue
		}
		if err := h.Validate(values, targetView, linkedView); err != nil {
			return fmt.Errorf("hook %s: %w", h.Name, err)
		}
	}
	return nil
}

func (s *Store) lookupIdem(obj ObjectID, key string) (idemRecord, bool) {
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	rec, ok := s.idem[string(obj)+"\x00"+key]
	return rec, ok
}

func (s *Store) putIdem(obj ObjectID, key string, rec idemRecord) {
	s.idemMu.Lock()
	s.idem[string(obj)+"\x00"+key] = rec
	s.idemMu.Unlock()
}
